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
// It also owns claimNext, the payload-scoped claim dispatch, relocated here
// when the PayloadNotMatch exclusion pushed worker.go past the strict 600-line
// gate. Keeping it in this file avoids adding a production file to the
// registered jobs hotspot (whose carry-forward debt must never increase).
package jobs

import (
	"context"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// claimNext claims the next eligible job for this worker. An empty payload
// match AND empty payload exclusion is the historical unscoped claim; a
// positive match requires kernel/job.PayloadScopedClaimer and an exclusion
// requires kernel/job.PayloadExcludingClaimer. Every scoped form fails closed
// rather than widening, so a dedicated phase pool can never silently claim the
// jobs it exists to avoid.
func (w *Worker) claimNext(ctx context.Context) (*job.Job, error) {
	if len(w.match) == 0 && len(w.notMatch) == 0 {
		return w.repo.ClaimNext(ctx, w.id, w.leaseTTL, w.types)
	}
	if len(w.notMatch) > 0 {
		scoped, ok := w.repo.(job.PayloadExcludingClaimer)
		if !ok {
			return nil, fmt.Errorf("worker %s: repository %T cannot honour payload_not_match %v", w.id, w.repo, w.notMatch)
		}
		return scoped.ClaimNextMatchingExcluding(ctx, w.id, w.leaseTTL, w.types, w.match, w.notMatch)
	}
	scoped, ok := w.repo.(job.PayloadScopedClaimer)
	if !ok {
		return nil, fmt.Errorf("worker %s: repository %T cannot honour payload_match %v", w.id, w.repo, w.match)
	}
	return scoped.ClaimNextMatching(ctx, w.id, w.leaseTTL, w.types, w.match)
}

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
