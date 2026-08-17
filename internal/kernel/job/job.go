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

// Status is the canonical extended-state job lifecycle.
//
//	queued → leased → running → waiting_children → finalizing → succeeded
//	                                                       ↘ retry_wait → queued
//	                                                       ↘ failed / cancelled
//
// FASE 1 (July 2026): StatusWaitingChildren is the canonical broker
// surface for the parent-aggregation window. Pre-FASE-1 wait-for-children
// was conveyed only via the application-level parent_state (job.Result
// ["parent_state"]) plus the typed parent_state_typed column, with the
// broker status stuck at RUNNING/FINALIZING/SUCCEEDED. Elevating the
// wait to a first-class broker status removes the silent-success
// failure mode where a parent was marked SUCCEEDED even though no
// child had been aggregated yet.
//
// Canonical sequence (audit 2026-07-03 P0 #4 closure):
//
//	QUEUED → RUNNING → WAITING_CHILDREN (worker fan-out complete)
//	WAITING_CHILDREN → FINALIZING (aggregator: all children terminal)
//	FINALIZING → SUCCEEDED | PARTIALLY_SUCCEEDED | FAILED | CANCELLED
type Status string

const (
	StatusQueued             Status = "QUEUED"
	StatusLeased             Status = "LEASED"
	StatusRunning            Status = "RUNNING"
	StatusWaitingChildren    Status = "WAITING_CHILDREN"
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
	case StatusQueued, StatusLeased, StatusRunning, StatusWaitingChildren, StatusFinalizing, StatusRetryWait,
		StatusSucceeded, StatusPartiallySucceeded, StatusIndexPending, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// ── Query filter (canonical) ────────────────────────────────────────

// Filter narrows job queries. All fields are optional; nil/zero means "don't filter".
type Filter struct {
	Status        *Status
	Type          *string
	WorkerID      string
	CorrelationID *string
	Limit         int
	Offset        int
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
	// ParentJobID is the canonical job-broker ID of the parent that fanned
	// this job out. Empty for root jobs. Persisted in the jobs table and
	// carried in the payload for remote claimers.
	ParentJobID string `json:"parent_job_id,omitempty"`
	// RootJobID is the top-level ancestor job ID (the fan-out root). For a
	// root job it equals ID; for a child it is inherited from the parent at
	// enqueue time. It is the canonical correlation key for derived
	// projections (performance_runs.root_job_id).
	RootJobID string `json:"root_job_id,omitempty"`
	// ParentStateTyped is the AUTHORITATIVE source for parent_job's
	// application-level state (godlike/06 SSOT — one canonical column per
	// fact). Added by migration 129 (P1.2 typed-state column migration).
	// The EXPAND-phase write-side dual-write in
	// internal/infrastructure/database/sqlite/jobs/repository_lifecycle.go::FinalizeAggregateParent
	// populates this column atomically with the JSON result column;
	// readers (PR-P1.2-SQL-DUAL-WRITE, July 2026) prefer this column
	// over the JSON resultMap["parent_state"] with JSON fallback during
	// the BACKFILL window (so pre-P1.2 rows without the typed column
	// continue to work). Post-CUTOVER (forward-pointer, deadline TBD)
	// the JSON key is retired; this column becomes the SOLE source.
	//
	// godlike/07 minimal-blast-radius: the zero value is the empty
	// string (matches the migration's DEFAULT '' contract). A reader
	// that sees "" must fall back to the JSON resultMap["parent_state"]
	// (per the BACKFILL contract in
	// internal/application/voiceover/jobs/parent_aggregator_state.go).
	ParentStateTyped string `json:"parent_state_typed,omitempty"`
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
