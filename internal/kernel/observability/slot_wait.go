package observability

import (
	"context"
	"time"
)

// AcquireSlot acquires a channel-backed semaphore and records the time spent
// blocked as a typed wait on the Run bound to ctx. A nil channel is treated as
// an unbounded slot. Cancellation is returned without acquiring a slot.
func AcquireSlot(ctx context.Context, sem chan struct{}, component ComponentName, kind WaitKind) (func(), error) {
	if sem == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now().UTC()
	select {
	case sem <- struct{}{}:
		finished := time.Now().UTC()
		if finished.After(started) {
			RecordWait(ctx, WaitInfo{Kind: kind, Component: component, StartedAt: started, FinishedAt: finished})
		}
		return func() { <-sem }, nil
	case <-ctx.Done():
		finished := time.Now().UTC()
		if finished.After(started) {
			RecordWait(ctx, WaitInfo{Kind: kind, Component: component, StartedAt: started, FinishedAt: finished})
		}
		return nil, ctx.Err()
	}
}
