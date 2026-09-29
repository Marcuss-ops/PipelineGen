package jobs

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

// TestSleepBackoffWithoutNotifierPollsInsteadOfPanicking pins the fallback for a
// worker wired without the wake-on-enqueue port.
//
// The failure this guards against is invisible by construction: the poll loop
// runs inside a fire-and-forget goroutine, so a nil-notifier dereference is
// recovered and reported as a panic line while the worker silently claims
// nothing — the queue keeps accepting jobs and none of them ever run. The
// fallback (fixed-interval polling) keeps such a worker functional.
func TestSleepBackoffWithoutNotifierPollsInsteadOfPanicking(t *testing.T) {
	w := NewWorker(WorkerDeps{
		ID:        "test-worker-no-notifier",
		Log:       zaptest.NewLogger(t),
		LeaseTTL:  time.Minute,
		PollEvery: time.Millisecond,
	})
	if w.notifier != nil {
		t.Fatal("this test must exercise the no-notifier path")
	}

	// Interval elapsed → the loop continues polling.
	if !w.sleepBackoff(context.Background(), 0) {
		t.Fatal("sleepBackoff must return true once the poll interval elapses")
	}

	// Cancellation still wins over the interval.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if w.sleepBackoff(ctx, time.Hour) {
		t.Fatal("sleepBackoff must return false on a cancelled context")
	}
}
