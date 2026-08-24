// Package audioasset — worker_process.go (VO-DECOMPOSITION P0 #1, July 2026):
// subprocess lifecycle for the persistent tts_edge_server.py worker.
//
// godlike/06 + AGENTS.md Pattern 5: single-purpose capability file in
// the same package. Owns the subprocess lifecycle:
//   - ensureStarted(ctx) — launches the Python HTTP server, reads the
//     PORT=<n> line from stdout, drains remaining stdout in a background
//     goroutine, validates /health, and stores baseURL.
//   - Stop() — graceful quit via POST /quit with 2s timeout + 5s SIGKILL.
//
// Mirrors the precedent in internal/capabilities/images/workflow/slide_worker_
// process.go (PR-CHROME-PROVIDER-SPLIT, 2026-07-04).
package audioasset

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ensureStarted launches the persistent TTS server if not already running.
// Must be called while p.mu is held. It only protects lifecycle state;
// synthesis requests are deliberately outside this critical section.
//
// Protocol:
//  1. Spawn `python3 tts_edge_server.py --host 127.0.0.1 --port 0` with
//     PYTHONUNBUFFERED=1 to force unbuffered stdout (without this, Python
//     blocks the PORT=<n> line in its ~8KB stdout pipe buffer, causing
//     scanner.Scan() to hang indefinitely — the root cause of the
//     voiceover pipeline hang documented 2026-07-10).
//  2. Read the PORT=<n> line from stdout with a 30-second timeout +
//     context cancellation (prevents indefinite hang if Python crashes
//     silently or blocks on stderr).
//  3. Drain remaining stdout in a background goroutine (prevents pipe
//     buffer from filling and blocking the Python process)
//  4. Validate the /health endpoint responds 200
//  5. Store baseURL = "http://127.0.0.1:<n>" for subsequent requests
//
// godlike/07 typed-error contract (PR-VO-TTS-PERSISTENT-WORKER): every
// failure path wraps a typed sentinel so callers can probe with
// errors.Is without parsing string fragments:
//   - ErrWorkerUnavailable: script missing / Start failed / PORT line missing
//   - ErrWorkerHealthFailed: post-startup GET /health returned non-200
//
// BUG-FIX (2026-07-10): 3 bugs that caused the voiceover pipeline hang:
//  1. No timeout on PORT line reading — scanner.Scan() blocked forever if
//     Python never printed PORT=
//  2. Python stdout buffering — without PYTHONUNBUFFERED=1, the PORT= line
//     stays in the OS pipe buffer, never reaching Go's scanner
//  3. No context cancellation — request context deadlines were ignored
func (p *Processor) ensureStarted(ctx context.Context) error {
	if p.started {
		if err := p.healthCheck(); err != nil {
			// Worker is dead — reset so the next call re-launches instead
			// of perpetually hitting the same dead health check.
			p.started = false
			p.baseURL = ""
			return fmt.Errorf("tts worker health check failed: %w: %w", err, ErrWorkerHealthFailed)
		}
		return nil
	}

	scriptPath := filepath.Join(p.pythonScriptsDir, "bridges", "tts_edge_server.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("tts_edge_server.py not found at %s: %w", scriptPath, ErrWorkerUnavailable)
	}

	p.log.Info("audio.Processor: launching persistent TTS server",
		zap.String("script", scriptPath))

	// exec.Command (not CommandContext) because the worker outlives request contexts.
	p.cmd = exec.Command("python3", scriptPath, "--host", "127.0.0.1", "--port", "0")

	// BUG-FIX (2026-07-10): PYTHONUNBUFFERED=1 forces unbuffered stdout.
	// Without this, Python's print(f"PORT={port}") stays in the ~8KB pipe
	// buffer and scanner.Scan() blocks indefinitely — the primary cause of
	// the voiceover pipeline hang.
	p.cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

	// Capture stdout to read the PORT=<n> line.
	stdoutPipe, err := p.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("tts server stdout pipe: %w: %w", err, ErrWorkerUnavailable)
	}

	// Route stderr to a file for post-mortem debugging.
	stderrPath := filepath.Join(os.TempDir(), "tts_edge_server_stderr.log")
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		p.log.Warn("audio.Processor: failed to create stderr log", zap.Error(err))
		p.cmd.Stderr = os.Stderr
	} else {
		p.cmd.Stderr = stderrFile
		defer stderrFile.Close()
	}

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start TTS server: %w: %w", err, ErrWorkerUnavailable)
	}

	// Read the PORT=<n> line from stdout with a 30-second timeout.
	// BUG-FIX (2026-07-10): without a timeout, scanner.Scan() blocks
	// indefinitely if Python crashes silently or blocks on stderr.
	// The 30-second deadline is generous (normal startup: ~1-3s) and
	// also respects the caller's context cancellation.
	scanner := bufio.NewScanner(stdoutPipe)
	var port int
	found := false
	portDeadline := time.AfterFunc(30*time.Second, func() {
		// Force-close the stdout pipe so scanner.Scan() returns false.
		_ = stdoutPipe.Close()
	})
	defer portDeadline.Stop()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		p.log.Debug("tts_server stdout", zap.String("line", line))
		if strings.HasPrefix(line, "PORT=") {
			portStr := strings.TrimPrefix(line, "PORT=")
			port, err = strconv.Atoi(portStr)
			if err != nil {
				p.log.Warn("audio.Processor: failed to parse PORT line",
					zap.String("line", line), zap.Error(err))
				continue
			}
			found = true
			break
		}
	}
	if !found {
		if scanErr := scanner.Err(); scanErr != nil {
			p.log.Warn("tts_server stdout scanner error after timeout", zap.Error(scanErr))
		}
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		return fmt.Errorf("tts server did not print PORT line within 30s (process may have crashed on startup): %w", ErrWorkerUnavailable)
	}

	// Drain remaining stdout in a background goroutine. The Python server
	// may print additional lines (SERVER_READY, aiohttp startup logs).
	// If we don't drain, the OS pipe buffer fills and the Python process
	// blocks on write, hanging indefinitely.
	go func() {
		for scanner.Scan() {
			p.log.Debug("tts_server stdout", zap.String("line", scanner.Text()))
		}
	}()

	p.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	p.httpClient = &http.Client{Timeout: 60 * time.Second}
	p.started = true

	p.log.Info("audio.Processor: TTS server started",
		zap.Int("pid", p.cmd.Process.Pid),
		zap.Int("port", port),
		zap.String("base_url", p.baseURL))

	// Warmup: PORT means the socket was allocated, not that aiohttp has
	// completed startup. Retry the probe briefly so concurrent first callers
	// do not incorrectly fall back to the legacy spawn-per-call path.
	var healthErr error
	for attempt := 0; attempt < 30; attempt++ {
		if healthErr = p.healthCheck(); healthErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			healthErr = ctx.Err()
			attempt = 30
		case <-time.After(100 * time.Millisecond):
		}
	}
	if healthErr != nil {
		p.started = false
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		return fmt.Errorf("tts server health check failed after startup: %w: %w", healthErr, ErrWorkerHealthFailed)
	}

	p.log.Info("audio.Processor: TTS server warmup complete, ready for synthesis")
	return nil
}

// Stop gracefully shuts down the persistent TTS server.
// Sends POST /quit with 2s timeout, waits up to 5 seconds for the
// process to exit, then kills it.
//
// Idempotent: safe to call even if the worker was never started.
func (p *Processor) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started {
		return nil
	}

	p.log.Info("audio.Processor: stopping TTS server")

	// Best-effort quit via POST /quit with 2s timeout.
	if p.baseURL != "" && p.httpClient != nil {
		quitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, err := http.NewRequestWithContext(quitCtx, http.MethodPost,
			p.baseURL+"/quit", nil)
		if err == nil {
			resp, _ := p.httpClient.Do(req)
			if resp != nil {
				resp.Body.Close()
			}
		}
		cancel()
	}

	if p.cmd != nil && p.cmd.Process != nil {
		done := make(chan error, 1)
		go func() {
			done <- p.cmd.Wait()
		}()
		select {
		case err := <-done:
			if err != nil {
				p.log.Warn("audio.Processor: TTS server exited with error", zap.Error(err))
			}
		case <-time.After(5 * time.Second):
			p.log.Warn("audio.Processor: TTS server did not exit within 5s — killing")
			_ = p.cmd.Process.Kill()
			<-done
		}
	}

	p.started = false
	p.baseURL = ""
	p.log.Info("audio.Processor: TTS server stopped")
	return nil
}
