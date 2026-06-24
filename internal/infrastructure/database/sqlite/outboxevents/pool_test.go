package outboxevents

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestPool_Stop_IsIdempotent locks in the contract that Pool.Stop() may
// be called multiple times (concurrently OR sequentially) without
// panicking on `close of closed channel`.
//
// Background: PR4.E followup commit 94853aa added a second
// SafeGo("cleanup-outbox-events-pool") caller in shutdown.go::buildCleanup
// which races with the lifecycle's SafeGo("outbox-events-shutdown")
// caller on ctx.Done(). Without the sync.Once guard, the race manifests
// as a panic surfacing in the cleanup-outbox-events-pool SafeGo
// goroutine label — see TestCleanupCanBeCalledMultipleTimesSafely for
// the system-level reproduction.
//
// This unit test pins the contract at the package boundary: Stop() must
// not panic on the second call. It uses nil *Repository + *HandlerRegistry
// because Pool.Stop does not dereference them — the panic in the original
// bug was purely the close-of-closed-channel race, not a DB issue.
//
// The deferred recover() catches any regression panic and converts it
// into a clean test failure (the test should pass cleanly with the
// sync.Once guard in place).
func TestPool_Stop_IsIdempotent(t *testing.T) {
	// Surface any regression panic from the second Stop as a clean
	// test failure. With the sync.Once guard, this deferred recover
	// never fires.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("second Stop() panicked: %v — sync.Once guard on close(p.stopChan) is missing or broken", r)
		}
	}()

	cfg := WorkerPollConfig{
		PollInterval:    100 * time.Millisecond,
		ProcessTimeout:  1 * time.Second,
		ReclaimInterval: 100 * time.Millisecond,
	}
	// nil repo + nil registry are safe: Pool.Stop does not touch them.
	// Start() is intentionally NOT invoked — wg.Wait() returns
	// immediately because no worker goroutines were Add'd.
	p := NewPool("test-idempotent-stop", nil, nil, zap.NewNop(), cfg)

	// First Stop(): closes stopChan (via sync.Once.Do); wg.Wait() returns
	// ~immediately since the WaitGroup counter is zero. Should succeed.
	if err := p.Stop(1 * time.Second); err != nil {
		t.Fatalf("first Stop() returned error: %v", err)
	}

	// Second Stop(): stopOnce.Do is a no-op (already executed); wg.Wait()
	// still returns ~immediately. Must not panic.
	//
	// Pre-fix this would panic with `close of closed channel` because
	// close(p.stopChan) ran twice. The deferred recover above would
	// catch it and t.Fatalf the test, exposing the regression.
	if err := p.Stop(1 * time.Second); err != nil {
		// An error here is acceptable (timeout / etc.) as long as
		// no panic surfaced; the panic detection is the load-bearing
		// assertion.
		t.Logf("second Stop() returned error (acceptable, no panic): %v", err)
	}
}

// TestPool_Stop_ConcurrentIsSafe covers the concurrent variant: two
// goroutines racing to call Stop() must produce exactly one close()
// (sync.Once guarantees that) and must not panic. Same nil-deps shim
// as TestPool_Stop_IsIdempotent above.
func TestPool_Stop_ConcurrentIsSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("concurrent Stop() panicked: %v — sync.Once guard on close(p.stopChan) is missing or broken", r)
		}
	}()

	cfg := WorkerPollConfig{
		PollInterval:    100 * time.Millisecond,
		ProcessTimeout:  1 * time.Second,
		ReclaimInterval: 100 * time.Millisecond,
	}
	p := NewPool("test-concurrent-stop", nil, nil, zap.NewNop(), cfg)

	// Two concurrent Stop() calls.
	stopped := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			stopped <- p.Stop(1 * time.Second)
		}()
	}

	// Wait for both. Errors are acceptable; panic detection is the
	// load-bearing assertion.
	for i := 0; i < 2; i++ {
		select {
		case err := <-stopped:
			t.Logf("concurrent Stop() call returned (acceptable): %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent Stop() did not return within 2s")
		}
	}
}
