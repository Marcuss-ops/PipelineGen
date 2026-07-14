// Package job — worker_commands.go (Card 9 Phase 2 SSOT extension, July 2026).
//
// Canonical worker command + capability DTOs that used to be declared as
// package-local types in internal/application/jobs/types.go. Per godlike/06
// single-owner-per-fact, these are kernel-level canonical (the runtime/worker
// surface) and exposed to other layers via the canonical chain.
//
// Phase 2 SSOT extension:
//   - WorkerCapabilities: the worker registration payload (capability list
//     advertised via /api/workers/capabilities).
//   - RegisterWorkerCommand, ClaimCommand, HeartbeatCommand, RenewCommand,
//     ProgressCommand, CompleteCommand, FailCommand: typed envelope structs
//     for the canonical 7 worker operations.
//   - CompleteWithArtifactsCommand: result+artifacts sidecar for atomic
//     finalisation through the JobFinalizer spine.
//
// Pattern: the canonical declaration lives here; consumers (application
// layer, infrastructure layer) reference these as either `kerneljob.X` or
// via the back-compat shim in internal/domain/job (which carries Go
// type-aliases that preserve identity at runtime).
package job

import "encoding/json"

// WorkerCapabilities is the worker registration payload advertised
// when the worker joins the cluster. The AuthoritativeAPI persists
// this on the workers table; the broker matches jobs to workers via
// the (intersection of advertised caps) AND (job.RequiredCapabilities).
type WorkerCapabilities struct {
	WorkerID    string     `json:"worker_id"`
	Advertised  []Capability `json:"advertised"`
	UpdatedAt   string     `json:"updated_at"`
}

// RegisterWorkerCommand is the capability-advertising command.
type RegisterWorkerCommand struct {
	WorkerID  string                  `json:"worker_id"`
	WorkerSessionID string           `json:"worker_session_id"`
	Capabilities WorkerCapabilities `json:"capabilities"`
}

// ClaimCommand is the broker-side enqueue path that picks the next
// job. The broker delivers a (jobID, leaseID) pair to the worker.
type ClaimCommand struct {
	WorkerID         string `json:"worker_id"`
	WorkerSessionID  string `json:"worker_session_id"`
	JobTypeFilter    string `json:"job_type_filter,omitempty"`
	MaxBatch         int    `json:"max_batch,omitempty"`
}

// HeartbeatCommand lets the worker extend its session lifecycle
// (used by long-running Create jobs that exceed the default lease TTL).
type HeartbeatCommand struct {
	WorkerID        string `json:"worker_id"`
	WorkerSessionID string `json:"worker_session_id"`
	JobID           string `json:"job_id"`
	LeaseID         string `json:"lease_id"`
}

// RenewCommand is the lease-only renewal (no Result/Error); callers
// use this for jobs that are still working but want to extend the lease.
type RenewCommand struct {
	WorkerID         string `json:"worker_id"`
	WorkerSessionID  string `json:"worker_session_id"`
	JobID            string `json:"job_id"`
	LeaseID          string `json:"lease_id"`
	ExpectedRevision int    `json:"expected_revision"`
	NewExpiration    string `json:"new_expiration"`
}

// ProgressCommand updates the job's progress field without changing
// status; observers (UI, metrics) read Job.Progress periodically.
type ProgressCommand struct {
	WorkerID         string `json:"worker_id"`
	WorkerSessionID  string `json:"worker_session_id"`
	JobID            string `json:"job_id"`
	LeaseID          string `json:"lease_id"`
	ExpectedRevision int    `json:"expected_revision"`
	Progress         int    `json:"progress"`
}

// CompleteCommand is the canonical SUCCEEDED transition (no artifacts).
// Workers that produce artifacts MUST use CompleteWithArtifactsCommand
// so the asset rows + outbox + SUCCEEDED transition are atomic.
type CompleteCommand struct {
	WorkerID         string          `json:"worker_id"`
	WorkerSessionID  string          `json:"worker_session_id"`
	JobID            string          `json:"job_id"`
	LeaseID          string          `json:"lease_id"`
	ExpectedRevision int             `json:"expected_revision"`
	Result           json.RawMessage `json:"result"`
	CorrelationID    string          `json:"correlation_id,omitempty"`
}

// FailCommand is the canonical FAILED transition (no retries exhausted).
// Workers with retry budget remaining should use ScheduleRetryCommand
// (out of phase 2 scope; see godlike/06 retry-policy contract).
type FailCommand struct {
	WorkerID         string `json:"worker_id"`
	WorkerSessionID  string `json:"worker_session_id"`
	JobID            string `json:"job_id"`
	LeaseID          string `json:"lease_id"`
	ExpectedRevision int    `json:"expected_revision"`
	Error            string `json:"error"`
	CorrelationID    string `json:"correlation_id,omitempty"`
}

// Compile-time interface assertion: ensure these types are non-empty.
var (
	_ = (*WorkerCapabilities)(nil)
	_ = (*RegisterWorkerCommand)(nil)
	_ = (*ClaimCommand)(nil)
	_ = (*HeartbeatCommand)(nil)
	_ = (*RenewCommand)(nil)
	_ = (*ProgressCommand)(nil)
	_ = (*CompleteCommand)(nil)
	_ = (*FailCommand)(nil)
)
