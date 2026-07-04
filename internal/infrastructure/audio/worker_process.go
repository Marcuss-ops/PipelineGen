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
// Mirrors the precedent in internal/application/images/slide_worker_
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
// Must be called while p.mu is held.
//
// Protocol:
//  1. Spawn `python3 tts_edge_server.py --host 127.0.0.1 --port 0`
//  2. Read the PORT=<n> line from stdout
//  3. Drain remaining stdout in a background goroutine (prevents pipe
//     buffer from filling and blocking the Python process)
//  4. Validate the /health endpoint responds 200
//  5. Store baseURL = "http://127.0.0.1:<n>" for subsequent requests
func (p *Processor) ensureStarted(ctx context.Context) error {
	if p.started {
		if err := p.healthCheck(); err != nil {
			// Worker is dead — reset so the next call re-launches instead
			// of perpetually hitting the same dead health check.
			p.started = false
			p.baseURL = ""
			return fmt.Errorf("tts worker health check failed: %w", err)
		}
		return nil
	}

	scriptPath := filepath.Join(p.pythonScriptsDir, "bridges", "tts_edge_server.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("tts_edge_server.py not found at %s", scriptPath)
	}

	p.log.Info("audio.Processor: launching persistent TTS server",
		zap.String("script", scriptPath))

	// exec.Command (not CommandContext) because the worker outlives request contexts.
	p.cmd = exec.Command("python3", scriptPath, "--host", "127.0.0.1", "--port", "0")

	// Capture stdout to read the PORT=<n> line.
	stdoutPipe, err := p.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("tts server stdout pipe: %w", err)
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
		return fmt.Errorf("failed to start TTS server: %w", err)
	}

	// Read the PORT=<n> line from stdout.
	scanner := bufio.NewScanner(stdoutPipe)
	var port int
	found := false
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
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		return fmt.Errorf("tts server did not print PORT line (process may have crashed on startup)")
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
	p.httpClient = &http.Client{Timeout: 5 * time.Minute}
	p.started = true

	p.log.Info("audio.Processor: TTS server started",
		zap.Int("pid", p.cmd.Process.Pid),
		zap.Int("port", port),
		zap.String("base_url", p.baseURL))

	// Warmup: validate /health responds 200.
	if err := p.healthCheck(); err != nil {
		p.started = false
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		return fmt.Errorf("tts server health check failed after startup: %w", err)
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
