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

// WorkerSession identifies a registered worker identity. A new registration
// always produces a new session ID.
type WorkerSession struct {
	WorkerID         string             `json:"worker_id"`
	SessionID        string             `json:"session_id"`
	SessionExpiresAt time.Time          `json:"session_expires_at"`
	Capabilities     WorkerCapabilities `json:"capabilities"`
	Version          string             `json:"version"`
	Hostname         string             `json:"hostname"`
}

// Lease represents a claimed job lease.
type Lease struct {
	Job       *Job      `json:"job,omitempty"`
	LeaseID   string    `json:"lease_id"`
	ExpiresAt time.Time `json:"expires_at"`
}
