package jobs

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

type Runner struct {
	store   Store
	registry *Registry
	logger   *zap.Logger
}

func NewRunner(store Store, registry *Registry, logger *zap.Logger) *Runner {
	return &Runner{
		store:   store,
		registry: registry,
		logger:   logger,
	}
}

func (r *Runner) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("runner stopped")
			return
		default:
		}

		job, err := r.store.LeaseNext(ctx)
		if err != nil {
			r.logger.Error("failed to lease next job", zap.Error(err))
			time.Sleep(5 * time.Second)
			continue
		}

		if job == nil {
			time.Sleep(2 * time.Second)
			continue
		}

		r.processJob(ctx, job)
	}
}

func (r *Runner) processJob(ctx context.Context, job *Job) {
	handler, ok := r.registry.Get(job.Type)
	if !ok {
		r.logger.Error("no handler registered for job type", zap.String("type", job.Type))
		if err := r.store.MarkFailed(ctx, job.ID, fmt.Errorf("no handler for job type: %s", job.Type)); err != nil {
			r.logger.Error("failed to mark job failed", zap.Error(err))
		}
		return
	}

	r.logger.Info("processing job", zap.String("id", job.ID), zap.String("type", job.Type))

	result, err := handler(ctx, job.PayloadJSON)
	if err != nil {
		r.logger.Error("job failed", zap.String("id", job.ID), zap.Error(err))
		r.handleFailure(ctx, job, err)
		return
	}

	if err := r.store.MarkSucceeded(ctx, job.ID, result); err != nil {
		r.logger.Error("failed to mark job succeeded", zap.Error(err))
		return
	}

	r.logger.Info("job succeeded", zap.String("id", job.ID))
}

func (r *Runner) handleFailure(ctx context.Context, job *Job, err error) {
	if job.Attempts+1 >= job.MaxAttempts {
		if markErr := r.store.MarkFailed(ctx, job.ID, err); markErr != nil {
			r.logger.Error("failed to mark job failed", zap.Error(markErr))
		}
		return
	}

	if err := r.store.IncrementAttempts(ctx, job.ID); err != nil {
		r.logger.Error("failed to increment attempts", zap.Error(err))
		return
	}

	if err := r.store.MarkRetrying(ctx, job.ID); err != nil {
		r.logger.Error("failed to mark job retrying", zap.Error(err))
		return
	}

	if err := r.store.MarkQueued(ctx, job.ID); err != nil {
		r.logger.Error("failed to requeue job", zap.Error(err))
	}
}

func (r *Runner) RecoverZombieJobs(ctx context.Context, timeout time.Duration) (int64, error) {
	return r.store.RecoverZombieJobs(ctx, timeout)
}

func (r *Runner) RunSingle(ctx context.Context) bool {
	job, err := r.store.LeaseNext(ctx)
	if err != nil {
		r.logger.Error("failed to lease next job", zap.Error(err))
		return false
	}

	if job == nil {
		return false
	}

	r.processJob(ctx, job)
	return true
}

func (r *Runner) RunWithWorkerCount(ctx context.Context, workerCount int) {
	for i := 0; i < workerCount; i++ {
		go func() {
			r.Run(ctx)
		}()
	}

	<-ctx.Done()
}
