package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// WorkflowStepCompletedHandler logs every workflow step completion as a
// structured audit event. The payload is parsed but not persisted —
// downstream audit pipelines (audit_log table, Datadog log stream)
// can subscribe by filtering on these structured fields.
//
// Real DB writes would risk amplifying retry storms under transactional
// pressure; audit logs are an outbox-best-effort side effect.
type WorkflowStepCompletedHandler struct {
	log *zap.Logger
}

// NewWorkflowStepCompletedHandler creates a handler with the supplied logger.
func NewWorkflowStepCompletedHandler(log *zap.Logger) *WorkflowStepCompletedHandler {
	return &WorkflowStepCompletedHandler{log: log}
}

// EventType returns "workflow.step.completed".
func (h *WorkflowStepCompletedHandler) EventType() string {
	return outboxevents.EventWorkflowStepCompleted
}

// IdempotencyKey implements outboxevents.Handler (Fase 6(c) Push 6.2).
// Static canonical form: `<event_type>.audit.v1` — schema_version literal
// is implicit (audit-only payload shape, not versioned). Operator can
// identify the handler class by the shape.
func (h *WorkflowStepCompletedHandler) IdempotencyKey() string {
	return outboxevents.EventWorkflowStepCompleted + ".audit.v1"
}

// workflowStepPayload is the schema of workflow.step.* payloads.
// Both completed and failed share this shape; status is the differentiator.
type workflowStepPayload struct {
	WorkflowID     string `json:"workflow_id"`
	StepID         string `json:"step_id"`
	Status         string `json:"status"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	DurationMillis int64  `json:"duration_ms,omitempty"`
	ResultSummary  string `json:"result_summary,omitempty"`
	ActorWorkerID  string `json:"actor_worker_id,omitempty"`
}

// Handle parses the event payload and emits a structured audit log.
// Returns nil on parse success — the event is MarkedCompleted. Returns
// an error on malformed JSON so the event goes through retry/dead_letter.
func (h *WorkflowStepCompletedHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	var p workflowStepPayload
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &p); err != nil {
		h.log.Warn("workflow.step.completed payload parse failed — sending to retry",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(err),
		)
		return fmt.Errorf("workflow.step.completed payload parse: %w", err)
	}

	h.log.Info("workflow step completed",
		zap.String("workflow_id", p.WorkflowID),
		zap.String("step_id", p.StepID),
		zap.String("status", p.Status),
		zap.String("aggregate_id", evt.AggregateID),
		zap.String("correlation_id", p.CorrelationID),
		zap.Int64("duration_ms", p.DurationMillis),
		zap.String("actor_worker_id", p.ActorWorkerID),
		zap.Int64("event_id", evt.ID),
		zap.Int("attempt", evt.AttemptCount),
	)
	return nil
}
