package observability

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// AcquireFairSlot acquires one slot of a per-owner-fair semaphore and records
// the time spent blocked as a typed wait on the Run bound to ctx — the same
// contract as AcquireSlot, for gates where cross-owner fairness matters.
//
// Why it exists (measured, 2026-09-28): a plain channel gate is FIFO over
// acquisitions, so a job pipelining 20 Drive uploads queued 20 acquirers ahead
// of another job's single upload and made it wait 85.7 s for ~5 s of work. The
// wait was ALSO invisible: it was charged to the upload's own work time. This
// helper fixes both ends — the gate serves a starving owner first, and the wait
// is recorded as WaitSemaphore so it leaves the work time and lands in the
// run's blocked interval. A nil semaphore is an unbounded slot. Cancellation is
// returned without acquiring a slot.
func AcquireFairSlot(ctx context.Context, sem *concurrent.FairSemaphore, owner string, component ComponentName, kind WaitKind) (func(), error) {
	if sem == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now().UTC()
	release, err := sem.AcquireCtx(ctx, owner)
	finished := time.Now().UTC()
	if finished.After(started) {
		RecordWait(ctx, WaitInfo{Kind: kind, Component: component, StartedAt: started, FinishedAt: finished})
	}
	return release, err
}

// WaitOwner names the work unit that competes for a fair gate. It is the run id
// bound to ctx, so two concurrent jobs never share an owner and cannot starve
// each other; when no run is bound (tests, detached helpers) the empty owner is
// returned and those callers share one identity under the same fairness rule.
func WaitOwner(ctx context.Context) string {
	run := FromContext(ctx)
	if run == nil {
		return ""
	}
	report := run.Report()
	if report == nil {
		return ""
	}
	return report.RunID
}

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
