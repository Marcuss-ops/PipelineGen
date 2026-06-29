package background

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// TestDetachWithTimeout_PreservesParentCancelRemoval: a detached ctx
// MUST survive cancellation of its parent; the parent cancel must
// not propagate to the detached scope.
func TestDetachWithTimeout_PreservesParentCancelRemoval(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	detached, cancel := DetachWithTimeout(parent, "test-survive", 5*time.Second)
	defer cancel()

	parentCancel()

	select {
	case <-detached.Done():
		t.Fatalf("detached context cancelled by parent cancel (must survive)")
	default:
		// Expected: detached is still alive after parent cancel.
	}
}

// TestDetachWithTimeout_AppliesTimeout: detached ctx MUST expire
// after the supplied timeout; cancel func MUST allow early release.
func TestDetachWithTimeout_AppliesTimeout(t *testing.T) {
	parent := context.Background()
	start := time.Now()
	detached, cancel := DetachWithTimeout(parent, "test-timeout", 100*time.Millisecond)
	defer cancel()

	select {
	case <-detached.Done():
		elapsed := time.Since(start)
		if elapsed > 500*time.Millisecond {
			t.Fatalf("timeout fired too late (expected ~100ms, got %v)", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout did not fire within 2s")
	}
}

// TestDetachWithTimeout_RegistersInFlight: the helper MUST register
// (taskName, traceID) in the global registry on entry so operators
// can count live detached tasks for metrics/dashboards.
func TestDetachWithTimeout_RegistersInFlight(t *testing.T) {
	parent := corid.WithCorrelationID(context.Background(), "test-trace-abc-123")
	detached, cancel := DetachWithTimeout(parent, "test-register", 1*time.Second)
	defer cancel()
	_ = detached

	key := "test-register:test-trace-abc-123"
	if _, ok := inFlight.Load(key); !ok {
		t.Fatalf("registry missing key %q after DetachWithTimeout", key)
	}
}

// TestDetachWithTimeout_NoTraceIDIsAllowed: empty trace id from ctx
// is tolerated — the key becomes "<taskName>:" (trailing colon)
// rather than panicking.
func TestDetachWithTimeout_NoTraceIDIsAllowed(t *testing.T) {
	parent := context.Background()
	detached, cancel := DetachWithTimeout(parent, "test-no-trace", 1*time.Second)
	defer cancel()
	_ = detached
	key := "test-no-trace:"
	if _, ok := inFlight.Load(key); !ok {
		t.Fatalf("registry missing key %q when trace ID empty", key)
	}
}

// TestDetachWithTimeout_NilCtxDefensiveFallback: nil parent ctx MUST
// not nil-deref; helper falls back to context.Background() per the
// pkg/contextutil/postwrite.go precedent.
func TestDetachWithTimeout_NilCtxDefensiveFallback(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil ctx must not panic (got panic: %v)", r)
		}
	}()
	detached, cancel := DetachWithTimeout(nil, "test-nil-ctx", 1*time.Second) //nolint:staticcheck // explicit nil-ctx test
	defer cancel()
	if detached == nil {
		t.Fatalf("detached ctx must be non-nil even with nil parent")
	}
}

// TestInFlight_ReflectsRegistry: exported counter MUST reflect the
// number of entries currently in the sync.Map (registry-cleanup-hook
// is a follow-up; this test pins the current write-only increment
// behaviour).
func TestInFlight_ReflectsRegistry(t *testing.T) {
	before := InFlight()
	parent := corid.WithCorrelationID(context.Background(), "test-count-trace")
	detached, cancel := DetachWithTimeout(parent, "test-count", 1*time.Second)
	defer cancel()
	_ = detached
	if got := InFlight(); got != before+1 {
		t.Fatalf("InFlight counter: before=%d, after register=%d, want before+1=%d", before, got, before+1)
	}
}
