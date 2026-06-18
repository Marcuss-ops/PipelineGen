// Package job defines the canonical domain types for the job system.
//
// These are the single source of truth for job status, filtering, and entity
// representation. The legacy models package (internal/media/models) still
// exists for backward-compat with the HTTP layer and will be migrated in
// Passaggio 6.
package job

import (
	"encoding/json"
	"time"
)

// Status is the canonical 5-state job lifecycle.
//
//   queued    → waiting for a worker
//   running   → worker is executing
//   completed → finished successfully (terminal)
//   failed    → exhausted retries or non-retryable error (terminal)
//   cancelled → operator cancelled (terminal)
//
// Allowed transitions:
//
//	queued    → running, cancelled
//	running   → completed, failed, cancelled, queued (retry / lease expiry)
//	failed    → queued (retry)
//	completed → (terminal)
//	cancelled → (terminal)
//
// Legacy state mapping (models.JobStatus):
//
//	models.StatusPending   → job.StatusQueued
//	models.StatusLeased    → not exposed — internal ClaimNext detail
//	models.StatusRunning   → job.StatusRunning
//	models.StatusSucceeded → job.StatusCompleted
//	models.StatusRetryWait → internal — represented as queued + retry_count>0
//	models.StatusFailed    → job.StatusFailed
//	models.StatusCancelled → job.StatusCancelled
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Filter narrows job queries. All fields are optional; nil/zero means
// "don't filter".
type Filter struct {
	Status   *Status
	Type     *string
	WorkerID string
	Limit    int
	Offset   int
}

// Job is the canonical domain entity for a job in the system.
//
// Differences from models.Job:
//   - Type is string (not models.JobType) — domain-agnostic
//   - Status is job.Status (not models.JobStatus)
//   - Result is json.RawMessage (not map[string]any)
//   - LeaseExpiry is carried directly for lease-fencing operations
type Job struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Status         Status          `json:"status"`
	Priority       int             `json:"priority"`
	Project        string          `json:"project,omitempty"`
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
