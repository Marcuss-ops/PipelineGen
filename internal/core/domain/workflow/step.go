package workflow

import "time"

type StepStatus string

const (
	StepBlocked   StepStatus = "blocked"
	StepReady     StepStatus = "ready"
	StepQueued    StepStatus = "queued"
	StepRunning   StepStatus = "running"
	StepCompleted StepStatus = "completed"
	StepFailed    StepStatus = "failed"
	StepSkipped   StepStatus = "skipped"
	StepCancelled StepStatus = "cancelled"
)

type Step struct {
	ID           string     `json:"id"`
	WorkflowID   string     `json:"workflow_id"`
	Key          string     `json:"step_key"`
	Type         string     `json:"step_type"`
	Status       StepStatus `json:"status"`
	Position     int        `json:"position"`
	JobID        string     `json:"job_id,omitempty"`
	AttemptCount  int        `json:"attempt_count"`
	MaxAttempts  int        `json:"max_attempts"`
	InputJSON    []byte     `json:"input_json,omitempty"`
	OutputJSON   []byte     `json:"output_json,omitempty"`
	ErrorCode    string     `json:"error_code,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	AvailableAt  *time.Time `json:"available_at,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
