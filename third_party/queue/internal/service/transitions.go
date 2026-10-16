package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

// Rendered marks a running job as rendered: the artifact is durably stored but
// external publication failed, so the job stays out of `completed` and is
// re-claimable for a publication-only retry.
func (s *Service) Rendered(ctx context.Context, id, workerID string, artifact model.Artifact, reason string) error {
	if err := validateArtifact(artifact); err != nil {
		return err
	}
	if err := s.repo.Rendered(ctx, id, workerID, artifact, reason); err != nil {
		return err
	}
	s.observePending(ctx)
	s.notify.Notify()
	return nil
}

// Fail marks a running job failed (requeue or permanent fail).
func (s *Service) Fail(ctx context.Context, id, workerID, reason string) error {
	if err := s.repo.Fail(ctx, id, workerID, reason); err != nil {
		return err
	}
	s.observePending(ctx)
	s.notify.Notify()
	return nil
}

// FailPermanently records a non-retryable failure as terminal, without
// consuming the ordinary attempt retry budget.
func (s *Service) FailPermanently(ctx context.Context, id, workerID, reason string) error {
	permanentRepo, ok := s.repo.(repository.PermanentFailureRepository)
	if !ok {
		return fmt.Errorf("permanent failure transition is not supported by repository")
	}
	if err := permanentRepo.FailPermanently(ctx, id, workerID, reason); err != nil {
		return err
	}
	s.observePending(ctx)
	s.notify.Notify()
	return nil
}

// Renew extends the lease for a running job owned by workerID.
func (s *Service) Renew(ctx context.Context, id, workerID string) error {
	return s.repo.Renew(ctx, id, workerID)
}

// RequeueExpired requeues (or permanently fails) jobs whose lease elapsed,
// returning the number of jobs affected. Transient repository failures are
// retried with exponential backoff and jitter (see SetRequeueRetry).
//
// ctx bounds the retry loop: the backoff between attempts waits on ctx as well
// as on the timer, so a shutdown (or a caller-imposed deadline) interrupts a
// sweep instead of parking the goroutine for the whole retry budget. A
// cancelled wait reports the context error — the sweep did NOT complete, and a
// caller must not read "0 jobs" from it.
func (s *Service) RequeueExpired(ctx context.Context, now time.Time) (int, error) {
	maxAttempts := s.retry.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var n int
	var err error
	for attempt := 1; ; attempt++ {
		n, err = s.repo.RequeueExpired(ctx, now)
		if err == nil || attempt >= maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(backoffDelay(s.retry, attempt)):
		}
	}
	if err != nil {
		return 0, err
	}
	if n > 0 && s.metrics != nil {
		s.metrics.LeaseExpired.Add(float64(n))
	}
	if n > 1 {
		// Multi-job path: n jobs became claimable at once, so wake every
		// parked waiter instead of one (a single wake would under-notify).
		s.notify.Broadcast()
	} else if n > 0 {
		s.notify.Notify()
	}
	s.observePending(ctx)
	return n, nil
}

// Retry resets a failed job back to pending state.
func (s *Service) Retry(ctx context.Context, id string) error {
	if err := s.repo.Retry(ctx, id); err != nil {
		return err
	}
	s.notify.Notify()
	s.observePending(ctx)
	return nil
}

// Cancel moves a job that has not reached a terminal state to cancelled.
// Cancelled is terminal: claim never selects it and lease expiry never
// requeues it, so the job can never be re-rendered across lease/claim cycles.
// Cancelling an already-cancelled job is a no-op.
func (s *Service) Cancel(ctx context.Context, id string) error {
	if err := s.repo.Cancel(ctx, id); err != nil {
		return err
	}
	// Wake claim waiters: a cancelled pending job is no longer claimable, so a
	// blocked claim must re-check the store instead of sleeping on a job that
	// will never arrive.
	s.notify.Notify()
	s.observePending(ctx)
	return nil
}

// SetProgress records the worker's render progress for a running job. The
// queue is the source of truth: progress survives worker restarts and is
// visible through GET /jobs/{id} without asking the worker directly.
func (s *Service) SetProgress(ctx context.Context, id, workerID string, p model.Progress) error {
	return s.repo.SetProgress(ctx, id, workerID, p)
}

// Stats returns a snapshot of the queue state.
func (s *Service) Stats(ctx context.Context) model.Stats {
	return s.repo.Stats(ctx)
}
