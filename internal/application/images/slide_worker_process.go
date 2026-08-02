// Package images — slide_worker_process.go (PR-CHROME-PROVIDER-SPLIT, 2026-07 + commit 5):
// subprocess lifecycle for the persistent Playwright slide_worker.py worker.
//
// PR-CHROME-PROVIDER-SPLIT (commit 5, July 2026): per godlike/06 SSOT,
// slide_worker_process.go is the SINGLE canonical owner of "the
// happy path" subprocess lifecycle (start + warmup + quit). The
// recovery detection (isDeadWorkerError) and state reset (resetWorker)
// are extracted to slide_worker_recovery.go so detection logic and
// launch/quit logic can evolve independently.
//
// After the split this file contains ONLY:
//
//   - ensureStarted(ctx) — launches the Playwright worker in single-profile
//     mode (--profiles 1 hard-coded; the legacy numProfiles constructor
//     arg that silently overrode this is RETIRED per godlike/07 no-fake-
//     availability, see chrome_provider.go package doc). On launch this
//     method DELIVERS the warmup command and waits for "ready".
//   - Stop() — graceful quit over stdin + 5s SIGKILL fallback.
//
// Recovery probing (isDeadWorkerError) and state reset (resetWorker)
// delegate to slide_worker_recovery.go. Pipe invalidation and
// dead-process detection lives there so this file can stay focused on
// the launch/quit happy path.
//
// Imports needed by this file (single-purpose slice per Pattern 5): the
// canonical ChromeImageProvider fields (p.cmd, p.stdin, p.stdout, p.started)
// + the JSON protocol helpers (p.writeJSON, p.readRawResponse) declared
// in slide_worker_protocol.go.
package images

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// ensureStarted launches the persistent worker if not already running.
// Must be called while p.mu is held.
//
// If the worker was previously started but the process has died (broken
// pipe, EOF on stdout, etc.), this method detects the failure via
// slide_worker_recovery.go::isDeadWorkerError, calls resetWorker
// (also slide_worker_recovery.go), and relaunches the subprocess.
// This prevents a permanently stuck provider after the Python worker
// crashes (OOM, SIGKILL, unhandled exception).
func (p *ChromeImageProvider) ensureStarted(ctx context.Context) error {
	if p.started {
		err := p.healthCheck()
		if err == nil {
			return nil
		}
		// Health check failed — the worker may have died.
		if p.isDeadWorkerError(err) {
			p.log.Warn("ChromeImageProvider: worker died, relaunching",
				zap.Error(err))
			p.resetWorker()
			// Fall through to relaunch below.
		} else {
			// Health check failed for a non-pipe reason (e.g. worker
			// returned unhealthy status). Don't relaunch — surface the error.
			return fmt.Errorf("worker unhealthy: %w", err)
		}
	}

	scriptPath := filepath.Join(p.scriptsDir, "bridges", "slide_worker.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("slide_worker.py not found at %s", scriptPath)
	}

	p.log.Info("ChromeImageProvider: launching persistent worker",
		zap.String("script", scriptPath))

	// Create the subprocess in single-profile mode (CANONICAL POLICY).
	// PR-CHROME-PROVIDER-SPLIT (2026-07): `--profiles 1` is hard-coded
	// here. The pre-split NewChromeImageProvider(scriptsDir, numProfiles,
	// log) accepted a numProfiles arg that this call site ignored (the
	// arg was fake-availability per godlike/07). Single-profile is the
	// canonical policy; the legacy numProfiles constructor arg is RETIRED
	// (see chrome_provider.go package doc for the godlike/07 rationale).
	//
	// We use exec.Command (not CommandContext) because the worker outlives
	// individual request contexts.
	p.cmd = exec.Command("python3", scriptPath, "--profiles", "1", "--profile-id", strconv.Itoa(p.profileID))

	var err error
	p.stdin, err = p.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := p.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	p.stdoutPipe = stdoutPipe
	p.stdout = bufio.NewScanner(stdoutPipe)
	// The worker writes one JSON line per response; the default 64 KB
	// bufio buffer is ample — a response is at most ~500 bytes.

	// Route stderr to a file for post-mortem debugging.
	stderrPath := filepath.Join(os.TempDir(), "slide_worker_stderr.log")
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		p.log.Warn("ChromeImageProvider: failed to create stderr log", zap.Error(err))
		p.cmd.Stderr = os.Stderr
	} else {
		p.cmd.Stderr = stderrFile
		// The child process holds a reference to the file descriptor.
		// Once cmd.Start() succeeds, the OS will close it when the child exits.
		// If cmd.Start() fails, we close it here to avoid a leak.
		defer stderrFile.Close()
	}

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start worker: %w", err)
	}

	p.started = true
	p.log.Info("ChromeImageProvider: worker process started",
		zap.Int("pid", p.cmd.Process.Pid))

	// Wait for the automatic startup "ready" response.
	resp, err := p.readRawResponse()
	if err != nil {
		return fmt.Errorf("warmup response failed: %w", err)
	}
	if resp["status"] != "ready" {
		return fmt.Errorf("warmup: expected status=ready, got %v", resp["status"])
	}

	p.log.Info("ChromeImageProvider: worker warmup complete, ready for generation")
	return nil
}

// Stop gracefully shuts down the persistent worker. Sends the quit
// command over stdin, waits up to 5 seconds for the process to exit,
// then kills it.
//
// Idempotent: safe to call even if the worker was never started.
//
// PR-CHROME-PROVIDER-SPLIT (2026-07, commit 5): the pre-extraction
// cooldowns map cleanup at the end of Stop() is REMOVED. The cooldowns
// field no longer exists on the struct (RETIRED per godlike/07 no-fake-
// availability, see chrome_provider.go package doc). The state-clearing
// sequence remains: started = false (so the next Generate() call
// re-warms the worker).
func (p *ChromeImageProvider) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.started {
		return nil
	}

	p.log.Info("ChromeImageProvider: stopping worker")
	// Best-effort quit; the worker may already be dead.
	_ = p.writeJSON(map[string]any{"action": "quit"})

	if p.stdin != nil {
		_ = p.stdin.Close()
		p.stdin = nil
	}
	if p.stdoutPipe != nil {
		_ = p.stdoutPipe.Close()
		p.stdoutPipe = nil
	}

	if p.cmd != nil && p.cmd.Process != nil {
		// Wait with timeout: if the worker doesn't exit within 5 seconds,
		// send SIGKILL to prevent shutdown from hanging forever.
		done := make(chan error, 1)
		go func() {
			done <- p.cmd.Wait()
		}()
		select {
		case err := <-done:
			if err != nil {
				p.log.Warn("ChromeImageProvider: worker exited with error", zap.Error(err))
			}
		case <-time.After(5 * time.Second):
			p.log.Warn("ChromeImageProvider: worker did not exit within 5s — killing")
			_ = p.cmd.Process.Kill()
			<-done // drain the Wait() goroutine
		}
	}

	p.started = false
	p.log.Info("ChromeImageProvider: worker stopped")
	return nil
}
