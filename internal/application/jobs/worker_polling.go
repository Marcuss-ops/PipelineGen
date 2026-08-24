// Package jobs — worker_polling.go (PR7 split, June 2026).
//
// Poll-loop BLOCKING helper extracted from worker.go. Owns:
//
//  1. func (w *Worker) sleepBackoff — blocks for duration d OR wakes
//     on the QueueNotifier's wake channel OR returns false on ctx
//     cancellation. Refreshes the notifier subscription on every call
//     so the post-Broadcast replacement channel is observed by the
//     next sleep iteration (close-and-replace invariant).
//
// The OUTER poll loop with the backoff state machine (consecutiveEmpty
// counter, currentBackoff escalation, etc.) is owned by
// worker.go::Start (per the PR7 spec, since Start is the canonical
// lifecycle entrypoint exported on *Worker).
//
// Mechanical split, zero behavior change. ONLY relocated +
// import-redistributed.
package jobs

import (
	"context"
	"time"

	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// sleepBackoff blocks for `d` OR wakes on the notifier's wake channel
// OR returns false on ctx cancellation. The notifier subscription is
// refreshed on every call so the post-Broadcast replacement channel is
// the one observed by the next sleep iteration (close-and-replace
// invariant).
func (w *Worker) sleepBackoff(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		d = w.pollEvery
	}
	wakeCh := w.notifier.Subscribe()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wakeCh:
		metrics.WorkerWakeOnEnqueueTotal.Inc()
		w.log.Debug("worker woke on enqueue broadcast",
			zap.String("worker_id", w.id))
		return true
	case <-timer.C:
		return true
	}
}
