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
	return jobqueue.NewService(s.repo, s.registry, consumers, s.log).Enqueue(ctx, req)
}
