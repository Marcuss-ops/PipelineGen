package operations

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainops "github.com/Marcuss-ops/PipelineGen/internal/domain/operations"
)

// persistSubmission creates the operation, job, and outbox event in one TX.
func (s *Service) persistSubmission(
	ctx context.Context,
	req SubmitRequest,
	prior *domainops.Operation,
) (*SubmitResult, error) {
	now := s.nowFunc()

	tx, err := s.txMgr.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("operations.Submit: BeginTx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	operationID := req.OperationID
	if operationID == "" {
		operationID = s.opIDGen()
	}
	jobID := req.JobID
	if jobID == "" {
		jobID = s.jobIDGen()
	}

	newOp := &domainops.Operation{
		OperationID:    operationID,
		Scope:          req.Scope,
		IdempotencyKey: req.IdempotencyKey,
		RequestHash:    req.RequestHash,
		JobID:          jobID,
		State:          domainops.StateQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if prior != nil {
		newOp.SupersedesOperationID = prior.OperationID
	}

	if err := s.ops.Insert(ctx, newOp, tx); err != nil {
		return nil, fmt.Errorf("operations.Submit: insert operation: %w", err)
	}
	if newOp.SupersedesOperationID != "" {
		if err := s.ops.UpdateState(
			ctx,
			newOp.SupersedesOperationID,
			domainops.StateSuperseded,
			tx,
		); err != nil {
			return nil, fmt.Errorf("operations.Submit: supersede prior: %w", err)
		}
	}

	newJob := &job.Job{
		ID:            jobID,
		Type:          req.JobType,
		Status:        job.StatusQueued,
		Priority:      req.JobPriority,
		Payload:       req.JobPayload,
		MaxRetries:    req.JobMaxRetries,
		RetryCount:    0,
		CorrelationID: req.IdempotencyKey,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.jobs.CreateInTx(ctx, tx, newJob); err != nil {
		return nil, fmt.Errorf("operations.Submit: insert job: %w", err)
	}

	payloadJSON, err := json.Marshal(map[string]string{
		"operation_id": operationID,
		"job_id":       jobID,
	})
	if err != nil {
		return nil, fmt.Errorf("operations.Submit: marshal outbox payload: %w", err)
	}
	if _, err := s.outbox.Enqueue(
		ctx,
		tx,
		EventTypeScriptGenerateQueued,
		operationID,
		aggregateTypeScriptGenerate,
		string(payloadJSON),
		operationID,
	); err != nil {
		return nil, fmt.Errorf("operations.Submit: enbox: %w", err)
	}

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
