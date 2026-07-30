package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// JobLookup is the narrow read port used to validate that a queued script
// event still points at a durable job row before the outbox event is acked.
type JobLookup interface {
	Get(context.Context, string) (*job.Job, error)
}

type ScriptGenerateQueuedHandler struct {
	jobs JobLookup
}

func NewScriptGenerateQueuedHandler(jobs JobLookup) (*ScriptGenerateQueuedHandler, error) {
	if jobs == nil {
		return nil, fmt.Errorf("script.generate.queued handler: jobs repository is required")
	}
	return &ScriptGenerateQueuedHandler{jobs: jobs}, nil
}

func (h *ScriptGenerateQueuedHandler) EventType() string {
	return outboxevents.EventScriptGenerateQueued
}

func (h *ScriptGenerateQueuedHandler) IdempotencyKey() string {
	return outboxevents.EventScriptGenerateQueued + ".job-validate.v1"
}

func (h *ScriptGenerateQueuedHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	var payload struct {
		OperationID string `json:"operation_id"`
		JobID       string `json:"job_id"`
	}
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("script.generate.queued payload: %w", err)
	}
	if payload.OperationID == "" || payload.JobID == "" {
		return fmt.Errorf("script.generate.queued payload missing operation_id or job_id")
	}
	j, err := h.jobs.Get(ctx, payload.JobID)
	if err != nil {
		return fmt.Errorf("script.generate.queued lookup job %q: %w", payload.JobID, err)
	}
	if j == nil {
		return fmt.Errorf("script.generate.queued references missing job %q", payload.JobID)
	}
	return nil
}
