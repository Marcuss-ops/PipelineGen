// worker_test.go — lifecycle tests for the persistent worker runner.
//
// The regression this file exists for: `ensure` starts a goroutine that drains
// the worker's stderr, and `reset` nils the `stderr` field. The pump used to
// read that field, so teardown raced with the pump — and a read that landed
// after the nil handed `io.Copy` a nil sink, which panics on the first Write
// instead of failing. Running this file with -race is what pins the fix; the
// `ResetAfterEnsure` case reproduces the window deterministically by tearing
// down immediately after the pump is created, before it is scheduled.
package rustworker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// stderrSpamWorker writes an executable script that answers exactly one request
// on stdout and then keeps writing diagnostics to stderr until it is killed.
//
// The endless stderr stream is the point: it guarantees the pump goroutine is
// alive (and still holding its stderr pipe) at the moment teardown runs, which
// is the interleaving the race detector needs to see.
//
// Skipped on Windows: the runner execs the binary directly, so the fixture is a
// POSIX shell script. The runner itself is platform-neutral; only the fixture
// is not.
func stderrSpamWorker(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture is a POSIX shell script; the runner under test is platform-neutral")
	}
	path := filepath.Join(t.TempDir(), "stderr-spam-worker.sh")
	script := "#!/bin/sh\n" +
		// One protocol response, then unbounded diagnostics on stderr.
		"printf 'ok\\n'\n" +
		"i=0\n" +
		"while [ \"$i\" -lt 100000 ]; do printf 'diag %s\\n' \"$i\" >&2; i=$((i+1)); done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fixture worker: %v", err)
	}
	return path
}

// TestPersistentRunner_ResetAfterEnsureIsRaceFree is the regression guard.
//
// It tears the runner down in the window where the pump has been started but
// has not necessarily run: before the fix, `reset` nilled `stderr` while the
// pump read it, which -race reports as a data race on the field and which can
// deliver a nil sink to io.Copy.
//
// The loop is deliberate. A single iteration may schedule the pump after the
// nil and miss the window; repeating it makes the detector reliable without
// adding sleeps or arbitrary synchronisation that would mask the bug.
func TestPersistentRunner_ResetAfterEnsureIsRaceFree(t *testing.T) {
	worker := stderrSpamWorker(t)

	for i := 0; i < 50; i++ {
		runner := &PersistentRunner{}
		if err := runner.ensure(worker, 1<<10); err != nil {
			t.Fatalf("iteration %d: ensure: %v", i, err)
		}
		// Teardown must be safe while the pump is in flight, and must return:
		// it waits for the pump instead of abandoning it against a discarded
		// buffer.
		runner.Reset()
	}
}

// TestPersistentRunner_RunThenResetDrainsStderr pins the supported lifecycle:
// a request returns the protocol line, the diagnostics the worker wrote reach
// the bounded tail, and Reset afterwards terminates cleanly.
func TestPersistentRunner_RunThenResetDrainsStderr(t *testing.T) {
	worker := stderrSpamWorker(t)
	runner := &PersistentRunner{}

	out, _, err := runner.Run(context.Background(), worker, []byte("{}\n"), 1<<20)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "ok" {
		t.Fatalf("response = %q, want %q", got, "ok")
	}

	// The pump runs concurrently with the request, so the snapshot Run returns
	// may legitimately predate the first copied diagnostic. Poll the live tail
	// instead of asserting on that instant: the contract under test is that the
	// diagnostics ARRIVE, not that they arrive before the response does.

	waitForStderrContaining(t, runner, "diag ")

	// Reset after a successful request must be a no-op teardown, not a hang.
	runner.Reset()

	// A fresh request after Reset starts a new process (the previous one was
	// killed), which is the documented contract of Reset.
	if _, _, err := runner.Run(context.Background(), worker, []byte("{}\n"), 1<<20); err != nil {
		t.Fatalf("Run after Reset: %v", err)
	}
	runner.Reset()
}

// waitForStderrContaining blocks until the runner's stderr tail contains want,
// or fails the test after a bounded deadline. Polling is the honest instrument
// here: the pump's progress is inherently concurrent with the caller, so any
// fixed sleep would either be flaky or slow for no reason.
func waitForStderrContaining(t *testing.T, runner *PersistentRunner, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if strings.Contains(string(runner.stderr.Bytes()), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stderr tail never contained %q: the pump dropped worker diagnostics", want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestPersistentRunner_ResetWithoutProcessIsSafe covers the shutdown path every
// adapter takes when it never started a worker: Reset must be idempotent and
// must not wait on a pump that was never started.
func TestPersistentRunner_ResetWithoutProcessIsSafe(t *testing.T) {
	runner := &PersistentRunner{}
	runner.Reset()
	runner.Reset()
}

// TestBoundedBuffer_RetainsTailAndMarksTruncation pins the retention contract
// the stderr tail depends on: the newest bytes survive, and a truncated tail
// says so rather than looking complete.
func TestBoundedBuffer_RetainsTailAndMarksTruncation(t *testing.T) {
	buffer := &BoundedBuffer{Limit: 64}
	if _, err := buffer.Write([]byte(strings.Repeat("a", 32))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := buffer.Write([]byte("TAIL")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := string(buffer.Bytes()); !strings.HasSuffix(got, "TAIL") {
		t.Fatalf("Bytes() = %q, want the newest bytes retained", got)
	}

	// Exceed the limit by more than the compaction quantum so the marking path
	// runs, and assert the result is bounded and self-describing.
	big := &BoundedBuffer{Limit: 1024}
	if _, err := big.Write([]byte(strings.Repeat("x", 64*1024))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := big.Bytes()
	if int64(len(got)) > 1024 {
		t.Fatalf("Bytes() length = %d, want <= limit 1024", len(got))
	}
	if !strings.Contains(string(got), "[output truncated]") {
		t.Fatalf("truncated tail must say so, got %q", got)
	}
}

// TestBoundedBuffer_ZeroLimitDiscards keeps the "no unbounded growth" rule: a
// non-positive limit is a discard sink, not an accidental full capture.
func TestBoundedBuffer_ZeroLimitDiscards(t *testing.T) {
	buffer := &BoundedBuffer{}
	n, err := buffer.Write([]byte("anything"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len("anything") {
		t.Fatalf("Write returned %d, want %d", n, len("anything"))
	}
	if len(buffer.Bytes()) != 0 {
		t.Fatalf("Bytes() = %q, want empty for a discard sink", buffer.Bytes())
	}
}
