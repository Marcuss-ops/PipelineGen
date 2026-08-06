// Package images — slide_worker_recovery.go (commit 5, 2026-07):
// detection + state-reset for a dead slide_worker.py subprocess.
//
// PR-CHROME-PROVIDER-SPLIT (commit 5, July 2026): per godlike/06 SSOT,
// slide_worker_recovery.go is the SINGLE canonical owner of "is the
// worker dead, and if so, tear it down". slide_worker_process.go owns
// the "happy path" of starting + stopping; this file owns the
// "the worker crashed / pipe closed / process exited" recovery
// surface.
//
// Why resetWorker is a method on *ChromeImageProvider (NOT a
// separate service, per user spec constraint): resetWorker
// mutates four struct fields (p.cmd, p.stdin, p.stdout, p.started)
// atomically and emits a single log line. Lifting it into a
// separate service would force every recovery path through an
// interface boundary that adds zero observability value while
// introducing a parameter-explosion surface (the four mutated
// fields would all become arguments). godlike/06 SSOT: the
// canonical owner of "the worker's process state
// transitions" lives on the receiver itself.
package chrome

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

// isDeadWorkerError detects errors caused by a dead worker
// subprocess (broken pipe, EOF, process exited). These are
// recoverable by relaunching.
//
// Detection ladder (each rung adds an additional probe):
//  1. nil fast-path: a nil error is never a dead-worker
//     signal (return false immediately).
//  2. String sniff: "broken pipe" / "stdout closed
//     unexpectedly" / "scanner" / "stdin is nil" /
//     "token too long" — matches the canonical wrap
//     shapes from the writer/reader pipeline.
//  3. EPIPE probe: a zero-byte write to stdin triggers a
//     syscall.EPIPE if the pipe is torn. Tolerates
//     os.ErrClosed + "closed pipe" + "file already closed".
//  4. ProcessState check: p.cmd.ProcessState.Exited() —
//     only fires AFTER the OS reaped the child, so this
//     final rung catches the "process crashed silently"
//     case where the pipe probe didn't trip.
//
// Must be called while p.mu is held.
func (p *ChromeImageProvider) isDeadWorkerError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// Broken pipe: write to a closed stdin pipe.
	if strings.Contains(msg, "broken pipe") {
		return true
	}
	// Use the underlying syscall error for EPIPE detection.
	// (Go wraps syscall.EPIPE in os.PathError / fs.PathError for pipe writes.)
	if p.stdin != nil {
		// Probe: try a zero-byte write to stdin. If the pipe is dead,
		// this returns syscall.EPIPE.
		_, probeErr := p.stdin.Write([]byte{0})
		if probeErr != nil && (errors.Is(probeErr, os.ErrClosed) ||
			probeErr == syscall.EPIPE ||
			strings.Contains(strings.ToLower(probeErr.Error()), "broken pipe") ||
			strings.Contains(strings.ToLower(probeErr.Error()), "closed pipe") ||
			strings.Contains(strings.ToLower(probeErr.Error()), "file already closed")) {
			return true
		}
	}
	// stdin was nil (process exited, pipe closed, or never started).
	if strings.Contains(msg, "stdin is nil") {
		return true
	}
	// EOF / unexpected stdout close from readRawResponse.
	if strings.Contains(msg, "stdout closed unexpectedly") {
		return true
	}
	// bufio.Scanner returns "token too long" on malformed output.
	if strings.Contains(msg, "scanner") || strings.Contains(msg, "token too long") {
		return true
	}
	// Process has exited (ProcessState available).
	if p.cmd != nil && p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return true
	}
	return false
}

// resetWorker kills the old worker process (if any) and clears
// all state so the next ensureStarted() launches a fresh
// subprocess.
//
// This is the recovery seam for broken-pipe / dead-worker
// detection. The old process is killed (best-effort), pipes are
// closed, and the started flag is cleared.
//
// Returns a channel that is closed when the background Wait()
// completes (process fully reaped). Callers that need to know
// the process is gone can wait on the returned channel.
// Callers that don't care can ignore it.
//
// godlike/07 fail-closed primitive: even if every step of the
// teardown fails (kill refuses, pipes don't close), the
// state-mutation sequence must complete so the next
// ensureStarted() can succeed against a clean baseline.
func (p *ChromeImageProvider) resetWorker() <-chan struct{} {
	waitDone := make(chan struct{})

	if p.stdin != nil {
		_ = p.stdin.Close()
		p.stdin = nil
	}
	if p.stdoutPipe != nil {
		_ = p.stdoutPipe.Close()
		p.stdoutPipe = nil
	}
	p.stdout = nil

	if p.cmd != nil && p.cmd.Process != nil {
		// Kill the worker process group so a failed Playwright startup cannot
		// leave Chromium descendants orphaned under PID 1.
		killWorkerProcessGroup(p.cmd.Process)
		// Drain the Wait() to prevent zombie. Capture cmd into a local
		// variable because we nil p.cmd on the next line — the goroutine
		// must not dereference p.cmd after resetWorker returns.
		cmd := p.cmd
		go func() {
			_ = cmd.Wait()
			close(waitDone)
		}()
	} else {
		close(waitDone)
	}
	p.cmd = nil
	p.started = false
	p.log.Info("ChromeImageProvider: worker state reset, ready for relaunch")
	return waitDone
}
