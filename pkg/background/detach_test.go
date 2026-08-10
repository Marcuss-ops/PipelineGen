package background

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

type contextValueKey string

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

func TestDetachWithTimeout_PreservesValuesAndCorrelation(t *testing.T) {
	parent := context.WithValue(context.Background(), contextValueKey("request"), "request-value")
	parent = corid.WithCorrelationID(parent, "trace-preserved")

	detached, cancel := DetachWithTimeout(parent, "test-values", time.Second)
	defer cancel()

	if got := detached.Value(contextValueKey("request")); got != "request-value" {
		t.Fatalf("context value not preserved: got=%v", got)
	}
	if got := corid.FromContext(detached); got != "trace-preserved" {
		t.Fatalf("correlation id not preserved: got=%q", got)
	}
}

func TestDetachWithTimeout_RemovesParentDeadlineCancellation(t *testing.T) {
	parent, parentCancel := context.WithTimeout(context.Background(), time.Hour)
	defer parentCancel()

	detached, cancel := DetachWithTimeout(parent, "test-deadline", time.Second)
	defer cancel()
	parentCancel()

	select {
	case <-detached.Done():
		t.Fatalf("detached context cancelled by parent deadline/cancel")
	default:
	}
	if _, ok := detached.Deadline(); !ok {
		t.Fatalf("detached context must have the helper deadline")
	}
}

// TestDetachWithTimeout_AppliesTimeout: detached ctx MUST expire
// after the supplied timeout; cancel func MUST allow early release.
func TestDetachWithTimeout_ZeroTimeoutExpiresImmediately(t *testing.T) {
	before := InFlight()
	ctx, cancel := DetachWithTimeout(context.Background(), "test-zero-timeout", 0)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("zero timeout did not expire immediately")
	}
	waitForInFlight(t, before)
}

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
// can count live detached tasks for metrics/dashboards, and CancelFunc
// MUST remove the registration.
func TestDetachWithTimeout_RegistersInFlight(t *testing.T) {
	parent := corid.WithCorrelationID(context.Background(), "test-trace-abc-123")
	detached, cancel := DetachWithTimeout(parent, "test-register", 1*time.Second)
	defer cancel()
	_ = detached

	key := "test-register:test-trace-abc-123"
	if _, ok := inFlight.Load(key); !ok {
		t.Fatalf("registry missing key %q after DetachWithTimeout", key)
	}
	cancel()
	if _, ok := inFlight.Load(key); ok {
		t.Fatalf("registry retained key %q after CancelFunc", key)
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
// not nil-deref; helper falls back to context.Background().
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
// number of active detached tasks and return to its baseline after
// cancellation.
func TestInFlight_ReflectsRegistry(t *testing.T) {
	before := InFlight()
	parent := corid.WithCorrelationID(context.Background(), "test-count-trace")
	detached, cancel := DetachWithTimeout(parent, "test-count", 1*time.Second)
	defer cancel()
	_ = detached
	if got := InFlight(); got != before+1 {
		t.Fatalf("InFlight counter: before=%d, after register=%d, want before+1=%d", before, got, before+1)
	}
	cancel()
	if got := InFlight(); got != before {
		t.Fatalf("InFlight counter after cancel: got=%d, want baseline=%d", got, before)
	}
}

func waitForInFlight(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := InFlight(); got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("InFlight did not reach %d (got %d)", want, InFlight())
}

// TestDetachWithTimeout_TimeoutCleansRegistry verifies that timeout cleanup
// does not depend on callers remembering to invoke CancelFunc.
func TestDetachWithTimeout_TimeoutCleansRegistry(t *testing.T) {
	before := InFlight()
	ctx, cancel := DetachWithTimeout(context.Background(), "test-auto-timeout", 10*time.Millisecond)
	_ = cancel // intentionally not called: the watcher owns timeout cleanup.
	if got := InFlight(); got != before+1 {
		t.Fatalf("InFlight after registration: got=%d, want=%d", got, before+1)
	}
	<-ctx.Done()
	waitForInFlight(t, before)
}

// TestDetachWithTimeout_DuplicateKeysTrackEachTask verifies that concurrent
// tasks with the same task/correlation key are tracked independently.
func TestDetachWithTimeout_DuplicateKeysTrackEachTask(t *testing.T) {
	before := InFlight()
	parent := corid.WithCorrelationID(context.Background(), "duplicate-trace")
	first, firstCancel := DetachWithTimeout(parent, "test-duplicate", time.Second)
	second, secondCancel := DetachWithTimeout(parent, "test-duplicate", time.Second)
	key := "test-duplicate:duplicate-trace"
	if got := InFlight(); got != before+2 {
		t.Fatalf("InFlight for duplicate keys: got=%d, want=%d", got, before+2)
	}
	firstCancel()
	if _, ok := inFlight.Load(key); !ok {
		t.Fatalf("registry removed while duplicate task remained")
	}
	if got := InFlight(); got != before+1 {
		t.Fatalf("InFlight after first duplicate cancel: got=%d, want=%d", got, before+1)
	}
	secondCancel()
	waitForInFlight(t, before)
	<-first.Done()
	<-second.Done()
}

// TestDetachWithTimeout_CancelAndTimeoutRace stresses both cleanup paths.
func TestDetachWithTimeout_CancelAndTimeoutRace(t *testing.T) {
	before := InFlight()
	const tasks = 64
	cancels := make([]context.CancelFunc, 0, tasks)
	for i := 0; i < tasks; i++ {
		_, cancel := DetachWithTimeout(context.Background(), "test-race", 5*time.Millisecond)
		cancels = append(cancels, cancel)
	}
	if got := InFlight(); got != before+tasks {
		t.Fatalf("InFlight after stress registration: got=%d, want=%d", got, before+tasks)
	}

	var wg sync.WaitGroup
	for _, cancel := range cancels {
		cancel := cancel
		wg.Add(1)
		go func() {
			defer wg.Done()
			cancel()
			cancel()
		}()
	}
	wg.Wait()
	waitForInFlight(t, before)
}
