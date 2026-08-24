// Package operations — generation_submission_transaction.go is
// the SOLE file in this package that opens, commits, and rolls
// back the *sql.Tx. The canonical atomic-TX body (Steps 5-11 of
// the original Submit) lives here as the persistSubmit method.
//
// godlike/06 SSOT (single-transaction-owner): the submission
// service has exactly ONE owner of the *sql.Tx lifecycle —
// this file. Adding BeginTx / Commit / Rollback calls in any
// other file (e.g. generation_submission_decision.go) would
// shatter the atomic surface: the typed ports
// (OperationsRepository.Insert / UpdateState, JobEnqueuer.
// CreateInTx, OutboxEmitter.Enqueue) participate via the
// caller-owned *sql.Tx; if any helper opened its own tx, the
// atomic propagate-or-rollback invariant would be broken.
//
// godlike/06 SSOT (single-event_type): the event_type +
// aggregate_type strings emitted into outbox_events during the
// TX body live here as constants (outbox usage is exclusive to
// this file). Moving them elsewhere would fragment the
// outbox-enqueue contract.
//
// The body's options ordering (Step 7 → 8 → 9 → 10 → 11) is
// the canonical PR-COMPLETE-WORKER-BROAD-FIX + FASE 2 layout:
// operations row gets an ID first so the jobs row can FK it,
// the supersede-flip (when applicable) commits together with
// the new operations row (the SAME tx), the jobs row carries
// the canonical CorrelationID = idempotency_key, and the
// outbox event_key is the operation_id (UNIQUE on the outbox
// table → idempotent re-enqueue is a defensive no-op; the
// submitMu mutex + fresh ID generation guarantee no collisions
// within this process).
package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// FASE 2 canonical event_type for the outbox row that the
// submission service emits in the same atomic TX as the
// operations INSERT + jobs INSERT. The worker that drains
// the outbox reads this event_type to route the event to the
// canonical `script.generate` consumer.
const EventTypeScriptGenerateQueued = "script.generate.queued"

// aggregateTypeScriptGenerate is the canonical "scope" name
// carried on the outbox row's aggregate_type column. Mirrors
// the operations.Scope value (`"script.generate"`).
const aggregateTypeScriptGenerate = "script.generate"

// persistSubmit runs the FASE 2 atomic-TX body (Steps 5-11 of
// the canonical Submit envelope). The transaction boundary is
// declared here — defer tx.Rollback() guards every early-
// return / error path; the explicit tx.Commit() flips the
// committed=true flag to suppress the deferred Rollback.
//
// godlike/06 SSOT (single-transaction-owner): this is the SOLE
// place in the operations package that calls txMgr.BeginTx,
// tx.Commit, or tx.Rollback. The typed ports (s.ops.Insert,
// s.ops.UpdateState, s.jobs.CreateInTx, s.outbox.Enqueue)
// participate transparently via the caller-owned *sql.Tx.
//
// godlike/07 minimum-blast-radius: the service does NOT wake
// the worker pool post-COMMIT (the canonical 1-5s worker poll
// interval is the FASE 2 latency floor; the pre-FASE-2
// immediate-wake optimisation is dropped in favor of
// atomicity; the pre-FASE-2 jobs.Service.Enqueue is unchanged).
//
// event_key == operationID on the outbox emit: UNIQUE per
// Submit. The submitMu mutex + the fresh ID generation guarantee
// no collisions within this process. Inserted=true is therefore
// guaranteed; we ignore the EnqueueResult for FASE 2 (a future
// multi-process Submit would need to surface Inserted=false as
// a typed no-op).
func (s *Service) persistSubmit(ctx context.Context, req SubmitRequest, prior *Operation, now time.Time) (*SubmitResult, error) {
	// ── Step 6: begin TX (orchestrator-owned boundary) ────────────
	tx, err := s.txMgr.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("operations.Submit: BeginTx: %w", err)
	}
	// Rollback is a no-op after a successful Commit (driver-level),
	// so the deferred Rollback handles the early-return + error paths
	// uniformly. The double-rollback protection is the driver contract.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// ── Step 7: INSERT new operation ─────────────────────────────────
	operationID := req.OperationID
	if operationID == "" {
		operationID = s.opIDGen()
	}
	jobID := req.JobID
	if jobID == "" {
		jobID = s.jobIDGen()
	}

	newOp := &Operation{
		OperationID:    operationID,
		Scope:          req.Scope,
		IdempotencyKey: req.IdempotencyKey,
		RequestHash:    req.RequestHash,
		JobID:          jobID,
		State:          StateQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if prior != nil {
		// decideReplayOrFresh guarantees this branch only fires
		// when req.ForceRefresh == true (a non-supersede same-hash
		// path returned an idempotency hit before reaching here).
		newOp.SupersedesOperationID = prior.OperationID
	}

	if err := s.ops.Insert(ctx, newOp, tx); err != nil {
		return nil, fmt.Errorf("operations.Submit: insert operation: %w", err)
	}

	// ── Step 8: flip prior op to SUPERSEDED (if supersede) ──────────
	if newOp.SupersedesOperationID != "" {
		if err := s.ops.UpdateState(ctx, newOp.SupersedesOperationID, StateSuperseded, tx); err != nil {
			return nil, fmt.Errorf("operations.Submit: supersede prior: %w", err)
		}
	}

	// ── Step 9: INSERT jobs row ─────────────────────────────────────
	newJob := &job.Job{
		ID:            jobID,
		Type:          req.JobType,
		Status:        job.StatusQueued,
		Priority:      req.JobPriority,
		Payload:       req.JobPayload,
		MaxRetries:    req.JobMaxRetries,
		RetryCount:    0,
		CorrelationID: req.IdempotencyKey, // mirror: same value as idempotency_key for trace
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.jobs.CreateInTx(ctx, tx, newJob); err != nil {
		return nil, fmt.Errorf("operations.Submit: insert job: %w", err)
	}

	// ── Step 10: INSERT outbox_events row ───────────────────────────
	payloadJSON, err := json.Marshal(map[string]string{
		"operation_id": operationID,
		"job_id":       jobID,
	})
	if err != nil {
		return nil, fmt.Errorf("operations.Submit: marshal outbox payload: %w", err)
	}
	if _, err := s.outbox.Enqueue(
		ctx, tx,
		EventTypeScriptGenerateQueued,
		operationID, // aggregate_id
		aggregateTypeScriptGenerate,
		string(payloadJSON),
		operationID, // event_key (UNIQUE on the outbox table)
	); err != nil {
		return nil, fmt.Errorf("operations.Submit: enbox: %w", err)
	}

	// ── Step 11: commit ─────────────────────────────────────────────
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("operations.Submit: commit: %w", err)
	}
	committed = true

	s.log.Info("operations.Submit: success",
		zap.String("operation_id", newOp.OperationID),
		zap.String("job_id", newOp.JobID),
		zap.String("scope", string(req.Scope)),
		zap.String("idempotency_key", req.IdempotencyKey),
		zap.Bool("force_refresh", req.ForceRefresh),
		zap.Bool("is_supersede", newOp.SupersedesOperationID != ""),
	)

	return &SubmitResult{
		Operation:   newOp,
		Job:         newJob,
		IsSupersede: newOp.SupersedesOperationID != "",
	}, nil
}
