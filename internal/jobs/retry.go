package jobs

import (
	"context"
	"time"

	"go.uber.org/zap"
)

type RetryPolicy struct {
	MaxAttempts int
	Backoff     time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		Backoff:     5 * time.Second,
	}
}

type Retryer struct {
	store   Store
	logger  *zap.Logger
	policy  RetryPolicy
}

func NewRetryer(store Store, logger *zap.Logger, policy RetryPolicy) *Retryer {
	return &Retryer{
		store:  store,
		logger: logger,
		policy: policy,
	}
}

func (r *Retryer) RetryFailed(ctx context.Context, jobID string) error {
	job, err := r.store.Get(ctx, jobID)
	if err != nil {
		return err
	}

	if job.Status != JobStatusFailed {
		return nil
	}

	if job.Attempts >= job.MaxAttempts {
		return nil
	}

	if err := r.store.IncrementAttempts(ctx, jobID); err != nil {
		return err
	}

	if err := r.store.MarkRetrying(ctx, jobID); err != nil {
		return err
	}

	return r.store.MarkQueued(ctx, jobID)
}

func (r *Retryer) RetryAll(ctx context.Context) (int, error) {
	jobs, err := r.store.List(ctx, ListFilter{Status: statusPtr(JobStatusFailed)})
	if err != nil {
		return 0, err
	}

	count := 0
	for _, job := range jobs {
		if job.Attempts < job.MaxAttempts {
			if err := r.RetryFailed(ctx, job.ID); err != nil {
				r.logger.Error("failed to retry job", zap.String("id", job.ID), zap.Error(err))
				continue
			}
			count++
		}
	}

	return count, nil
}

func statusPtr(s JobStatus) *JobStatus {
	return &s
}
