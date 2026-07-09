package images

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ── isDeadWorkerError tests ──────────────────────────────────────────────

func TestIsDeadWorkerError_NilError(t *testing.T) {
	p := &ChromeImageProvider{log: zap.NewNop()}
	if p.isDeadWorkerError(nil) {
		t.Fatal("nil error should not be a dead worker error")
	}
}

func TestIsDeadWorkerError_BrokenPipeString(t *testing.T) {
	p := &ChromeImageProvider{log: zap.NewNop()}
	err := fmt.Errorf("write to pipe: broken pipe")
	if !p.isDeadWorkerError(err) {
		t.Fatal("'broken pipe' in error message should be detected as dead worker")
	}
}

func TestIsDeadWorkerError_StdoutClosedUnexpectedly(t *testing.T) {
	p := &ChromeImageProvider{log: zap.NewNop()}
	err := fmt.Errorf("worker stdout closed unexpectedly (process may have exited)")
	if !p.isDeadWorkerError(err) {
		t.Fatal("'stdout closed unexpectedly' should be detected as dead worker")
	}
}

func TestIsDeadWorkerError_ProcessExited(t *testing.T) {
	// Run a subprocess that exits immediately so we get a valid ProcessState.
	cmd := exec.Command("true")
	_ = cmd.Run() // ProcessState is now populated.

	p := &ChromeImageProvider{
		log: zap.NewNop(),
		cmd: cmd,
	}
	err := fmt.Errorf("some generic error")
	if !p.isDeadWorkerError(err) {
		t.Fatal("process with exited ProcessState should be detected as dead worker")
	}
}

func TestIsDeadWorkerError_HealthyWorker(t *testing.T) {
	// A worker that is still running (we simulate with a sleeping process).
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}
	defer cmd.Process.Kill()

	p := &ChromeImageProvider{
		log:     zap.NewNop(),
		cmd:     cmd,
		started: true,
	}
	err := fmt.Errorf("some unrelated error")
	if p.isDeadWorkerError(err) {
		t.Fatal("a healthy worker with a generic error should NOT be detected as dead")
	}
}

func TestIsDeadWorkerError_EPIPEViaWriteProbe(t *testing.T) {
	// Create a pipe and close the write end to simulate a broken stdin.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	r.Close() // close read end

	p := &ChromeImageProvider{
		log:   zap.NewNop(),
		stdin: w,
	}

	// Close the write end to simulate a broken pipe.
	w.Close()

	// Pass a generic error that does NOT contain "broken pipe" so that
	// the stdin probe (Write([]byte{0})) fires and detects EPIPE.
	// If we passed "broken pipe" in the message, the string-match would
	// short-circuit and the probe would never execute.
	probeErr := fmt.Errorf("health check failed: worker unresponsive")
	if !p.isDeadWorkerError(probeErr) {
		t.Fatal("EPIPE via stdin probe should be detected as dead worker")
	}
}

func TestIsDeadWorkerError_TokenTooLong(t *testing.T) {
	p := &ChromeImageProvider{log: zap.NewNop()}
	err := fmt.Errorf("bufio.Scanner: token too long")
	if !p.isDeadWorkerError(err) {
		t.Fatal("'token too long' should be detected as dead worker")
	}
}

func TestIsDeadWorkerError_StdinIsNil(t *testing.T) {
	p := &ChromeImageProvider{log: zap.NewNop(), stdin: nil}
	err := fmt.Errorf("health check: worker stdin is nil (process may have exited)")
	if !p.isDeadWorkerError(err) {
		t.Fatal("'stdin is nil' should be detected as dead worker")
	}
}

func TestIsDeadWorkerError_UnrelatedError(t *testing.T) {
	p := &ChromeImageProvider{log: zap.NewNop()}
	err := fmt.Errorf("warmup: expected status=ready, got error")
	if p.isDeadWorkerError(err) {
		t.Fatal("a non-pipe error should NOT be detected as dead worker")
	}
}

// ── resetWorker tests ────────────────────────────────────────────────────

func TestResetWorker_ClearsState(t *testing.T) {
	p := &ChromeImageProvider{
		log:     zap.NewNop(),
		started: true,
		stdin:   nil, // no real stdin needed for state-clearing test
		stdout:  nil,
	}
	p.resetWorker()
	if p.started {
		t.Fatal("resetWorker should set started=false")
	}
	if p.cmd != nil {
		t.Fatal("resetWorker should set cmd=nil")
	}
}

func TestResetWorker_KillsProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep: %v", err)
	}

	p := &ChromeImageProvider{
		log:     zap.NewNop(),
		cmd:     cmd,
		started: true,
	}
	done := p.resetWorker()

	// resetWorker() kills the process and spawns a background goroutine
	// that calls cmd.Wait(). The returned channel closes when Wait()
	// completes — giving us a proper synchronization point instead of
	// polling cmd.ProcessState (which is subject to memory-visibility races
	// between the goroutine writing it and the test reading it).
	select {
	case <-done:
		// Process fully reaped by the background goroutine.
	case <-time.After(2 * time.Second):
		t.Fatal("process was not killed or waited on by resetWorker within 2 seconds")
	}
}

// ── ensureStarted dead-worker detection test ─────────────────────────────

func TestEnsureStarted_DetectsDeadWorkerAndRelaunches(t *testing.T) {
	// This test verifies that ensureStarted detects a dead worker
	// (started=true but healthCheck fails with a dead-worker error)
	// and relaunches.
	//
	// We can't test with a real Python worker here (requires Playwright),
	// but we CAN verify the detection path by setting up a fake state:
	// started=true, a dead process, and verifying that resetWorker is called.

	// Run a subprocess that exits immediately.
	cmd := exec.Command("true")
	_ = cmd.Run() // ProcessState is now populated.

	p := &ChromeImageProvider{
		scriptsDir: "/nonexistent",
		log:        zap.NewNop(),
		started:    true,
		cmd:        cmd, // already exited
		stdin:      nil,
		stdout:     nil,
	}

	// ensureStarted should detect the dead worker (ProcessState.Exited)
	// and call resetWorker, then attempt to relaunch. Since scriptsDir
	// is nonexistent, the relaunch will fail with "slide_worker.py not found".
	// But the key assertion is that started was reset to false.
	err := p.ensureStarted(t.Context())
	if err == nil {
		t.Fatal("expected error (slide_worker.py not found), got nil")
	}
	if !strings.Contains(err.Error(), "slide_worker.py not found") {
		t.Fatalf("expected 'slide_worker.py not found' error, got: %v", err)
	}
	// Verify the worker was reset (started=false) even though relaunch failed.
	if p.started {
		t.Fatal("ensureStarted should have reset started=false after detecting dead worker")
	}
}
