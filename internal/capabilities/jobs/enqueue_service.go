// Package jobs keeps Service.Enqueue as the compatibility facade while the
// queue package owns enqueue/idempotency policy.
package jobs

import (
	"context"
	"fmt"

	jobqueue "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Enqueue delegates queue admission to the canonical queue service. Registry,
// dispatcher bindings and the broker remain centrally owned by root Service.
func (s *Service) Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	if s == nil {
		return nil, fmt.Errorf("jobs.Service.Enqueue: nil service")
	}
	var consumers jobqueue.ConsumerBindings
	if s.dispatcher != nil {
		consumers = s
	}
	q := jobqueue.NewService(s.repo, s.registry, consumers, s.log)
	// Deferred scheduling: when the broker supports it, a future-dated
	// request is persisted as SCHEDULED and promoted by the job scheduler.
	// Without the port, queue.Service.Enqueue fails closed on a future
	// ScheduledAt instead of running the job immediately.
	if s.scheduleStore != nil {
		q = q.WithScheduler(s.scheduleStore)
	}
	return q.Enqueue(ctx, req)
}
