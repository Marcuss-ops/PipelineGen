package workflowservice

import "encoding/json"

type CreateWorkflowCommand struct {
	Type           string
	Version        int
	CorrelationID  string
	IdempotencyKey string
	InputJSON      json.RawMessage
}

type StartWorkflowCommand struct {
	WorkflowID string
}

type AttachJobCommand struct {
	WorkflowID string
	StepID     string
	JobID      string
}

type StepResultCommand struct {
	WorkflowID string
	StepID     string
	OutputJSON json.RawMessage
}
