package job

import (
	"encoding/json"
	"time"
)

// Status represents the canonical job status.
type Status string

// Canonical job statuses (7-state machine, PR-2).
const (
	StatusPending    Status = "PENDING"
	StatusLeased     Status = "LEASED"
	StatusRunning    Status = "RUNNING"
	StatusSucceeded  Status = "SUCCEEDED"
	StatusRetryWait  Status = "RETRY_WAIT"
	StatusFailed     Status = "FAILED"
	StatusCancelled  Status = "CANCELLED"

	// Legacy aliases for backward compatibility with domain consumers.
	StatusQueued    = StatusPending
	StatusCompleted = StatusSucceeded
)

// Job represents a domain-level job.
type Job struct {
	ID             string
	Type           string
	Status         Status
	Priority       int
	Project        string
	Payload        json.RawMessage
	Result         json.RawMessage
	Error          string
	Progress       int
	RetryCount     int
	MaxRetries     int
	WorkerID       string
	CorrelationID  string
	WorkflowID     string
	WorkflowStepID string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
}

// Filter represents a job query filter.
type Filter struct {
	Status   *Status
	Type     *string
	WorkerID string
	Limit    int
	Offset   int
}
