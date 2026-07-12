package operations

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainops "github.com/Marcuss-ops/PipelineGen/internal/domain/operations"
)

func validateSubmitRequest(req SubmitRequest) error {
	if !req.Scope.IsValid() {
		return fmt.Errorf("%w: %q", domainops.ErrInvalidOperationScope, req.Scope)
	}
	if !domainops.IsValidIdempotencyKey(req.IdempotencyKey) {
		return domainops.ErrIdempotencyKeyInvalid
	}
	if !domainops.IsValidRequestHash(req.RequestHash) {
		return domainops.ErrRequestHashInvalid
	}
	if req.JobType == "" {
		return fmt.Errorf("operations.Submit: empty JobType")
	}
	if len(req.JobPayload) == 0 {
		return fmt.Errorf("operations.Submit: empty JobPayload")
	}
	return nil
}

// resolveSubmission loads the latest operation for the idempotency bucket and
// returns either a replay result or the prior operation to supersede.
func (s *Service) resolveSubmission(
	ctx context.Context,
	req SubmitRequest,
) (*domainops.Operation, *SubmitResult, error) {
	prior, err := s.ops.GetLatestForKey(ctx, req.Scope, req.IdempotencyKey, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("operations.Submit: lookup prior: %w", err)
	}

	// A superseded row is terminal history, not the active head of the bucket.
	if prior != nil && prior.State == domainops.StateSuperseded {
		prior = nil
	}

	if prior != nil && req.OperationID != "" && req.OperationID == prior.OperationID {
		return nil, nil, fmt.Errorf("%w: operation_id=%q",
			domainops.ErrSelfSupersedeReference, req.OperationID)
	}

	switch {
	case prior == nil:
		return nil, nil, nil
	case !req.ForceRefresh && prior.RequestHash == req.RequestHash:
		return nil, s.buildReplayResult(ctx, req, prior), nil
	case !req.ForceRefresh && prior.RequestHash != req.RequestHash:
		return nil, nil, domainops.WrapIdempotencyConflict(
			req.Scope, req.IdempotencyKey, prior.RequestHash, req.RequestHash)
	case req.ForceRefresh:
		return prior, nil, nil
	default:
		return nil, nil, fmt.Errorf(
			"operations.Submit: unreachable state (prior=%+v, force_refresh=%v)",
			prior,
			req.ForceRefresh,
		)
	}
}

func (s *Service) buildReplayResult(
	ctx context.Context,
	req SubmitRequest,
	prior *domainops.Operation,
) *SubmitResult {
	s.log.Info("operations.Submit: idempotency hit",
		zap.String("operation_id", prior.OperationID),
		zap.String("scope", string(req.Scope)),
		zap.String("idempotency_key", req.IdempotencyKey),
	)

	var canonicalJob *job.Job
	canonicalJob, err := s.jobGetter.Get(ctx, prior.JobID)
	if err != nil {
		s.log.Warn("operations.Submit: canonical job lookup failed on replay",
			zap.String("operation_id", prior.OperationID),
			zap.String("job_id", prior.JobID),
			zap.Error(err),
		)
	}

	return &SubmitResult{
		Operation:        prior,
		Job:              canonicalJob,
		IsIdempotencyHit: true,
	}
}
