// Package jobs — compatibility adapters for enqueue validation.
// The reusable request-boundary policy is owned by jobs/queue.
package jobs

import (
	jobqueue "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// MaxPayloadSize remains exported from the root package for existing
// consumers; jobs/queue is the canonical owner of the value.
const MaxPayloadSize = jobqueue.MaxPayloadSize

func validateEnqueueRequest(req *job.EnqueueRequest) error {
	return jobqueue.ValidateEnqueueRequest(req)
}
