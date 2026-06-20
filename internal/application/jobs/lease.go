package jobs

import (
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// Job is a type alias for the canonical domain Job entity.
// Allows Lease.Job (declared as *Job) to satisfy the post-refactor
// application-layer Lease struct without forcing every caller to
// rewrite `*job.Job` as a fully-qualified pointer.
type Job = job.Job

// WorkerSession is a type alias to the canonical domain WorkerSession entity.
// The canonical struct lives in internal/domain/job so that infrastructure and
// other cross-layer consumers can refer to job.WorkerSession without importing
// the application layer.
type WorkerSession = job.WorkerSession

// Lease represents a claimed job lease.
type Lease struct {
	Job       *Job      `json:"job,omitempty"`
	LeaseID   string    `json:"lease_id"`
	ExpiresAt time.Time `json:"expires_at"`
}
