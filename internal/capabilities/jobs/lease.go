package jobs

import (
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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

// WorkerCertIdentity is a type alias for the canonical domain cert identity
// (PR-0, June 2026). The canonical struct lives in internal/domain/job so
// api-layer callers see `appjobs.WorkerCertIdentity` without taking a
// direct import on the domain package. Cert fields NO longer live on
// WorkerSession (which is session-only); this alias gives the api
// helper (handler_workers_cert.go::FromSessionCertIdentity) the typed
// channel through which the cert identity reaches the CertReport JSON.
type WorkerCertIdentity = job.WorkerCertIdentity

// Lease represents a claimed job lease.
type Lease struct {
	Job       *Job      `json:"job,omitempty"`
	LeaseID   string    `json:"lease_id"`
	ExpiresAt time.Time `json:"expires_at"`
}
