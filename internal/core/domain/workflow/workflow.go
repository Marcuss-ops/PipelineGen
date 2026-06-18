package workflow

import "time"

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type Workflow struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	Version        int       `json:"version"`
	Status         Status    `json:"status"`
	CorrelationID  string    `json:"correlation_id,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	InputJSON      []byte    `json:"input_json"`
	OutputJSON     []byte    `json:"output_json,omitempty"`
	ErrorCode      string    `json:"error_code,omitempty"`
	ErrorMessage   string    `json:"error_message,omitempty"`
	Revision       int64     `json:"revision"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	CancelledAt    *time.Time `json:"cancelled_at,omitempty"`
}
