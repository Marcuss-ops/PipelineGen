package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// WorkflowStepFailedHandler logs every workflow step failure at ERROR
// level so on-call monitoring tools (Datadog, Sentry) pick it up
// immediately. Like the completed handler, it is best-effort and
// side-effect-only.
type WorkflowStepFailedHandler struct {
	log *zap.Logger
	// hookFn is an optional side-effect for downstream alerting
	// (PagerDuty, Slack). nil means no-op. Tests use a captured hookFn
	// instead of mocking the zap logger output.
	hookFn func(workflowID, stepID, errMsg string)
}

// NewWorkflowStepFailedHandler creates a handler with the supplied logger.
// hookFn is optional — pass nil for production (no downstream alerting sidecar).
func NewWorkflowStepFailedHandler(log *zap.Logger, hookFn func(workflowID, stepID, errMsg string)) *WorkflowStepFailedHandler {
	return &WorkflowStepFailedHandler{log: log, hookFn: hookFn}
}

// EventType returns "workflow.step.failed".
func (h *WorkflowStepFailedHandler) EventType() string {
	return outboxevents.EventWorkflowStepFailed
}

// IdempotencyKey implements outboxevents.Handler (Fase 6(c) Push 6.2).
// Static canonical form: `<event_type>.audit.v1`. mirror of
// WorkflowStepCompletedHandler; both share the audit-only shape.
func (h *WorkflowStepFailedHandler) IdempotencyKey() string {
	return outboxevents.EventWorkflowStepFailed + ".audit.v1"
}

// Handle parses the payload and emits an ERROR-level audit log.
// Returns nil on parse success — the failure itself is a terminal
// outbox event, not a handler failure.
func (h *WorkflowStepFailedHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	var p workflowStepPayload
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &p); err != nil {
		h.log.Warn("workflow.step.failed payload parse failed — sending to retry",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(err),
		)
		return fmt.Errorf("workflow.step.failed payload parse: %w", err)
	}

	h.log.Error("workflow step failed",
		zap.String("workflow_id", p.WorkflowID),
		zap.String("step_id", p.StepID),
		zap.String("status", p.Status),
		zap.String("aggregate_id", evt.AggregateID),
		zap.String("correlation_id", p.CorrelationID),
		zap.String("result_summary", p.ResultSummary),
		zap.String("actor_worker_id", p.ActorWorkerID),
		zap.Int64("event_id", evt.ID),
		zap.Int("attempt", evt.AttemptCount),
	)

	if h.hookFn != nil {
		h.hookFn(p.WorkflowID, p.StepID, p.ResultSummary)
	}
	return nil
}
