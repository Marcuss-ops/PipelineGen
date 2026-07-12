package images

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// ── ErrNoImageCandidate tests (P0.1, July 2026) ───────────────────────────
//
// godlike/07 FAIL-CLOSED contract: when the Python worker reports it could
// not extract a valid candidate (no candidate, only stale results, blob:/
// googleusercontent both unreachable), the Go side MUST:
//   - remove any file at the canonical output_path
//   - NOT return a GeneratedImage to the caller
//   - surface a typed error that wraps ErrImageGenNoImageCandidate
// so retry policies + audit logs can distinguish 'no candidate' from any
// other terminal failure. The pre-fix code fell back to a slide-export
// (File → Download → PNG) which produced blank/white artifacts that passed
// byte-level validation. The tests below lock in the FAIL-CLOSED contract.

// ClassifyError-pure unit tests — no process / pipe plumbing required.

func TestClassifyError_ErrNoImageCandidate(t *testing.T) {
	err := ClassifyError("ErrNoImageCandidate")
	if err == nil {
		t.Fatal("ClassifyError(\"ErrNoImageCandidate\") returned nil")
	}
	if !errors.Is(err, ErrImageGenNoImageCandidate) {
		t.Fatalf("ClassifyError should wrap ErrImageGenNoImageCandidate; got %v", err)
	}
}

func TestClassifyError_ErrNoImageCandidate_CaseInsensitive(t *testing.T) {
	// Worker may emit mixed-case; ClassifyError lowers the input on the
	// substring probe, so the surface is case-insensitive.
	cases := []string{
		"ErrNoImageCandidate",
		"errnoimagecandidate",
		"ERRNOIMAGECANDIDATE",
		"  ErrNoImageCandidate  ",
	}
	for _, c := range cases {
		err := ClassifyError(c)
		if err == nil {
			t.Fatalf("ClassifyError(%q) returned nil", c)
		}
		if !errors.Is(err, ErrImageGenNoImageCandidate) {
			t.Fatalf("ClassifyError(%q) must wrap ErrImageGenNoImageCandidate; got %v", c, err)
		}
	}
}

func TestClassifyError_ErrNoImageCandidate_NotOtherSentinels(t *testing.T) {
	err := ClassifyError("ErrNoImageCandidate")
	// Must NOT cross-classify into other typed sentinels (the pre-fix hit
	// the 'default' branch which mapped to ErrImageGenPermanent).
	for _, sentinel := range []error{
		ErrImageGenPermanent,
		ErrImageGenNetwork,
		ErrImageGenQuota,
		ErrImageGenAuth,
		ErrImageGenPolicy,
	} {
		if errors.Is(err, sentinel) {
			t.Fatalf("ErrNoImageCandidate must NOT be classified as %v; got %v", sentinel, err)
		}
	}
}

// Integration test: the full generateOnce path wires the worker →
// ChromeImageProvider FAIL-CLOSED contract. ensureStarted triggers
// healthCheck (1 request/response round-trip), then generateOnce writes a
// generate request and reads the matching-ID response. We mock both
// round-trips with canned JSON and assert: (a) typed ErrImageGenNoImageCandidate
// is returned, (b) any orphan file at outputPath is REMOVED, (c) the
// caller does NOT receive a GeneratedImage.

func TestGenerateOnce_ErrNoImageCandidate_FailClosedRemovesOutput(t *testing.T) {
	stdInR, stdInW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdin: %v", err)
	}
	stdOutR, stdOutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdout: %v", err)
	}
	// Review feedback P0.1: break the test-cycle after Generate returns.
	// Closing stdInW unblocks the drainer goroutine's bufio.Scanner
	// (os.Pipe does not return EOF until the writer end is closed),
	// which then closes the requestIds channel and unblocks the
	// response-writer goroutine. Without this seam, both goroutines
	// leak under `go test -race`.
	t.Cleanup(func() {
		_ = stdInW.Close()
	})

	// Background cmd so isDeadWorkerError does not trip on nil p.cmd
	// (ProcessState nil → not exited; the stdin probe Write succeeds
	// because stdInR is being drained by goroutine 1).
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("sleep start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	p := &ChromeImageProvider{
		log:     zap.NewNop(),
		stdin:   stdInW,
		stdout:  bufio.NewScanner(stdOutR),
		started: true,
		cmd:     cmd,
	}

	// Sentinel: orphan file at the canonical outputPath. The FAIL-CLOSED
	// contract says this MUST be removed when the worker reports an error.
	outputPath := filepath.Join(t.TempDir(), "extracted.png")
	if err := os.WriteFile(outputPath, []byte("orphan"), 0o644); err != nil {
		t.Fatalf("setup orphan: %v", err)
	}

	// Handshake channel: drainer goroutine forwards each request id
	// (empty for health, populated for generate); response-writer
	// goroutine waits until it sees a non-empty id and emits the matching
	// error response.
	requestIDs := make(chan string, 4)

	// Goroutine 1: drain stdin. Without consumption, every provider
	// writeJSON would block on the synchronous io.Pipe semantics.
	go func() {
		defer close(requestIDs)
		scanner := bufio.NewScanner(stdInR)
		for scanner.Scan() {
			var req map[string]any
			if json.Unmarshal(scanner.Bytes(), &req) != nil {
				continue
			}
			id, _ := req["id"].(string)
			select {
			case requestIDs <- id:
			default:
				// channel full → drainer ahead of writer; drop.
			}
		}
	}()

	// Goroutine 2: serve canned responses in the order the provider expects.
	go func() {
		defer stdOutW.Close()
		// 1) Health response: triggers ensureStarted → healthCheck success.
		if _, werr := fmt.Fprintln(stdOutW, `{"status":"ok"}`); werr != nil {
			return
		}
		// 2) Generate response: wait for the dynamic id from the drainer.
		var genID string
		for captured := range requestIDs {
			if captured != "" { // health request has no id; skip empties
				genID = captured
				break
			}
		}
		resp := fmt.Sprintf(
			`{"id":"%s","status":"error","code":"ErrNoImageCandidate","error":"ErrNoImageCandidate","profile":0}`+"\n",
			genID,
		)
		_, _ = stdOutW.Write([]byte(resp))
	}()

	// Run the test through the public Generate entry point so the
	// deadWorkerError retry logic also exercises the typed-error path
	// (it must NOT retry on ErrImageGenNoImageCandidate).
	g, genErr := p.Generate(context.Background(), GenerateImageRequest{
		Prompt:     "test prompt",
		Width:      1024,
		Height:     1024,
		OutputPath: outputPath,
	})

	// (a) No GeneratedImage surfaced.
	if g != nil {
		t.Fatalf("expected nil GeneratedImage on FAIL-CLOSED ErrNoImageCandidate; got %+v", g)
	}
	// (b) Typed sentinel propagated.
	if genErr == nil {
		t.Fatalf("expected error on FAIL-CLOSED ErrNoImageCandidate; got nil")
	}
	if !errors.Is(genErr, ErrImageGenNoImageCandidate) {
		t.Fatalf("expected err to wrap ErrImageGenNoImageCandidate; got %v", genErr)
	}
	// (c) Orphan file removed from outputPath.
	if _, statErr := os.Stat(outputPath); statErr == nil {
		t.Fatalf("outputPath MUST be REMOVED on FAIL-CLOSED; still exists")
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("outputPath unexpectd stat err: %v", statErr)
	}
}
