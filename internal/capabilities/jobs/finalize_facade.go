// Package jobs — compatibility facade for the finalization layer.
// The canonical implementation lives in jobs/finalize (the lower layer);
// this file re-exports its types, sentinels and constructors so existing
// jobs-root and wiring callers keep working unchanged while they migrate
// to the finalize package directly.
package jobs

import (
	"database/sql"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	fin "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/finalize"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// Type aliases to the canonical lower layer.
type (
	Finalizer                    = fin.Finalizer
	JobCompletionEvent           = fin.JobCompletionEvent
	Subscription                 = fin.Subscription
	JobCompletionBus             = fin.JobCompletionBus
	WorkflowStepCompletedHandler = fin.WorkflowStepCompletedHandler
	WorkflowStepFailedHandler    = fin.WorkflowStepFailedHandler
)

// Sentinel errors from the completion bus, canonically owned by jobs/finalize.
var (
	ErrSubscriptionClosed = fin.ErrSubscriptionClosed
	ErrWaitTimedOut       = fin.ErrWaitTimedOut
)

// New constructs the canonical Finalizer (the single terminal SUCCEEDED
// state writer); the implementation lives in jobs/finalize.
func New(db *sql.DB, outbox *outboxevents.Repository, assetTx finalization.AssetFinalizerTx, log *zap.Logger) *fin.Finalizer {
	return fin.New(db, outbox, assetTx, log)
}

// NewBus builds a completion bus that wakes API/CLI waiters when a job
// flips to its terminal success state.
func NewBus() fin.JobCompletionBus {
	return fin.NewBus()
}

// NewWorkflowStepCompletedHandler builds the outbox handler that records a
// completed workflow step.
func NewWorkflowStepCompletedHandler(log *zap.Logger) *fin.WorkflowStepCompletedHandler {
	return fin.NewWorkflowStepCompletedHandler(log)
}

// NewWorkflowStepFailedHandler builds the outbox handler that records a
// failed workflow step.
func NewWorkflowStepFailedHandler(log *zap.Logger, hookFn func(workflowID, stepID, errMsg string)) *fin.WorkflowStepFailedHandler {
	return fin.NewWorkflowStepFailedHandler(log, hookFn)
}
