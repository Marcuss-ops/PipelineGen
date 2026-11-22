package job

import (
	"context"
	"testing"
)

// TestWorkerStoppedRequiresAnInstalledProbe pins the fail-open default: a ctx
// without a probe reports false, so direct callers (tests, one-shot tooling)
// keep the historical failure semantics.
func TestWorkerStoppedRequiresAnInstalledProbe(t *testing.T) {
	t.Parallel()

	if WorkerStopped(nil) {
		t.Fatal("a nil context must not report a worker shutdown")
	}
	if WorkerStopped(context.Background()) {
		t.Fatal("a context without a probe must not report a worker shutdown")
	}
}

// TestWorkerStoppedFollowsTheLiveProbe pins that the probe is evaluated at call
// time, not captured once: shutdown can begin at any point during Dispatch.
func TestWorkerStoppedFollowsTheLiveProbe(t *testing.T) {
	t.Parallel()

	stopping := false
	ctx := WithWorkerStopProbe(context.Background(), func() bool { return stopping })
	if WorkerStopped(ctx) {
		t.Fatal("a live worker must not be reported as stopped")
	}
	stopping = true
	if !WorkerStopped(ctx) {
		t.Fatal("the probe must be read at call time")
	}
}

// TestWithWorkerStopProbeIgnoresNilInputs keeps the seam panic-free.
func TestWithWorkerStopProbeIgnoresNilInputs(t *testing.T) {
	t.Parallel()

	if got := WithWorkerStopProbe(nil, func() bool { return true }); got != nil {
		t.Fatal("a nil context must stay nil")
	}
	base := context.Background()
	if got := WithWorkerStopProbe(base, nil); got != base {
		t.Fatal("a nil probe must leave the context untouched")
	}
}
