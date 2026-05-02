package jobs

import "time"

type JobStatus string

const (
	JobStatusCreated   JobStatus = "created"
	JobStatusQueued    JobStatus = "queued"
	JobStatusRunning   JobStatus = "running"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
	JobStatusRetrying  JobStatus = "retrying"
)

type Job struct {
	ID          string
	Type        string
	Status      JobStatus
	PayloadJSON string
	ResultJSON  string
	Error       string
	Attempts    int
	MaxAttempts int
	CreatedAt   time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	UpdatedAt   time.Time
}
