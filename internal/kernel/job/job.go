// Package job is the kernel subzone for stable job-execution concepts
// shared by capabilities that produce or consume background work
// (canonical Job shape, JobType, Lease, terminal states, WorkerSession).
//
// Phase A.2 (June 2026): Status lifecycle + canonical Job entity
// migrated from internal/domain/job/ into the kernel subzone. The
// domain package preserves type aliases (type Job = kerneljob.Job, etc.)
// for back-compat with 107 import sites in 93 files across the codebase.
//
// Per godlike/02 kernel rules:
//   - Stdlib-only imports (no repository, no Gin, no transport).
//   - Canonical definitions; consumer capability code must import
//     `internal/kernel/job` (or `internal/domain/job` for back-compat).
package job

import (
	"encoding/json"
	"time"
)

// ── Status lifecycle (canonical) ────────────────────────────────────

// Status is the canonical 8-state job lifecycle.
//
//	queued → leased → running → finalizing → succeeded
//	                            ↘ retry_wait → queued
//	                            ↘ failed / cancelled
type Status string

const (
	StatusQueued             Status = "QUEUED"
	StatusLeased             Status = "LEASED"
	StatusRunning            Status = "RUNNING"
	StatusFinalizing         Status = "FINALIZING"
	StatusRetryWait          Status = "RETRY_WAIT"
	StatusSucceeded          Status = "SUCCEEDED"
	StatusPartiallySucceeded Status = "PARTIALLY_SUCCEEDED"
	StatusIndexPending       Status = "INDEX_PENDING"
	StatusFailed             Status = "FAILED"
	StatusCancelled          Status = "CANCELLED"
)

// IsTerminal returns true if the status is a final state.
// PARTIALLY_SUCCEEDED is terminal: the job finished, some artifacts
// succeeded and some failed. No further worker action is expected.
func (s Status) IsTerminal() bool {
	return s == StatusSucceeded || s == StatusPartiallySucceeded || s == StatusFailed || s == StatusCancelled
}

// IsActive returns true if a worker currently owns this job.
// FINALIZING is considered active: the worker still holds the lease
// and is performing cleanup (artifact publication, outbox writes).
// INDEX_PENDING is active when post-emission Qdrant projection is
// still trying to land: the canonical Qdrant-reconciler task owns
// the row until it either succeeds (→ Succeeded) or fails terminal
// (→ Failed).
func (s Status) IsActive() bool {
	return s == StatusLeased || s == StatusRunning || s == StatusFinalizing ||
		s == StatusIndexPending
}

// Valid returns true if s is a known job status.
func (s Status) Valid() bool {
	switch s {
	case StatusQueued, StatusLeased, StatusRunning, StatusFinalizing, StatusRetryWait,
		StatusSucceeded, StatusPartiallySucceeded, StatusIndexPending, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// ── Query filter (canonical) ────────────────────────────────────────

// Filter narrows job queries. All fields are optional; nil/zero means "don't filter".
type Filter struct {
	Status   *Status
	Type     *string
	WorkerID string
	Limit    int
	Offset   int
}

// ── Canonical Job entity (kernel, ≥2-capability) ───────────────────

// Job is the canonical domain entity for a job in the system.
//
// Migrated from internal/domain/job/ in Phase A.2 (June 2026). The
// domain package re-exports as `type Job = kerneljob.Job` (alias, transparent).
type Job struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Status         Status          `json:"status"`
	Priority       int             `json:"priority"`
	Project        string          `json:"project,omitempty"`
	VideoName      string          `json:"video_name,omitempty"`
	ActiveKey      string          `json:"active_key,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	Error          string          `json:"error,omitempty"`
	Progress       int             `json:"progress"`
	RetryCount     int             `json:"retry_count"`
	MaxRetries     int             `json:"max_retries"`
	WorkerID       string          `json:"worker_id,omitempty"`
	LeaseID        string          `json:"lease_id,omitempty"`
	LeaseExpiry    *time.Time      `json:"lease_expiry,omitempty"`
	Revision       int             `json:"revision"`
	CorrelationID  string          `json:"correlation_id,omitempty"`
	WorkflowID     string          `json:"workflow_id,omitempty"`
	WorkflowStepID string          `json:"workflow_step_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	CancelledAt    *time.Time      `json:"cancelled_at,omitempty"`
}

// IsTerminal returns true if the job has reached a terminal state.
func (j *Job) IsTerminal() bool {
	if j == nil {
		return false
	}
	return j.Status.IsTerminal()
}

// CanRetry checks if the job can be retried.
func (j *Job) CanRetry() bool {
	if j == nil {
		return false
	}
	return j.RetryCount < j.MaxRetries && (j.Status == StatusFailed || j.Status == StatusRetryWait)
}

// ── Canonical Event entity (kernel, ≥2-capability) ──────────────────

// Event represents a discrete event in a job's timeline.
//
// Migrated from internal/domain/job/ in Phase A.2 (June 2026). The
// domain package re-exports as `type Event = kerneljob.Event` (alias, transparent).
type Event struct {
	ID        string         `json:"id"`
	JobID     string         `json:"job_id"`
	Type      string         `json:"type"`
	Message   string         `json:"message"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
