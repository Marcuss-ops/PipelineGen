// Package operations — generation_submission_decision.go is the
// per-responsibility helper file for the FASE 2 submission
// flow's pre-transaction decision layer:
//
//   - validateSubmitRequest — pure-function input guard
//     (godlike/07 fail-closed at the application boundary).
//   - lookupPriorOperation — typed-port read with the
//     StateSuperseded-= nil fix (Push 2.2a HIGH code-review
//     fix).
//   - decideReplayOrFresh — classifies the post-lookup state
//     into hit / conflict / fresh / force-refresh-supersede;
//     returns (*SubmitResult, mustPersist bool, error).
//
// godlike/06 SSOT (single-owner): this file owns NO
// transaction boundaries. txMgr.BeginTx / tx.Commit /
// tx.Rollback appear ONLY in generation_submission_transaction.go.
// Decision logic here is intentionally side-effect-free
// before the TX: it emits the typed "idempotency hit" log + reads
// the canonical live Job via JobGetter (also a READ, no
// mutation). The TX body lives in persistSubmit.
package operations

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	domainops "github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// validateSubmitRequest is the canonical FASE 2 input guard.
// godlike/07 fail-closed: any input violation is rejected at
// the boundary before the mutex is acquired. NO DB write.
// Free function (no Service receiver) keeps the validation
// testable in isolation and prevents the validation logic from
// accidentally reaching into the WHILE-MUTEX-HELD critical
// section below.
//
// Validation contract (regression-locked by
// generation_submission_service_test.go):
//   - Scope must be in the canonical enum
//     (domainops.Scope.IsValid); else ErrInvalidOperationScope.
//   - IdempotencyKey must pass
//     domainops.IsValidIdempotencyKey; else
//     ErrIdempotencyKeyInvalid.
//   - RequestHash must pass domainops.IsValidRequestHash
//     (64-char lowercase hex); else ErrRequestHashInvalid.
//   - JobType non-empty; JobPayload non-empty (raw JSON bytes).
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

// lookupPriorOperation reads the canonical prior operation
// for the (scope, key) bucket via the typed-port
// OperationsRepository.GetLatestForKey. The StateSuperseded-=nil
// fix (Push 2.2a HIGH code-review fix) collapses a terminal
// SUPERSEDED prior to nil so the next decision layer treats it
// as "no prior" — the user already ran a force_refresh and the
// prior was replaced. Without the fix, a SUPERSEDED prior would
// surface as an idempotency hit (return stale terminal op) or
// chain a new supersede link to the wrong row.
//
// Called WITH submitMu held by Submit. No TX → just a read;
// passing nil for the tx argument is the typed-port contract for
// "no caller-owned transaction". Returns (nil, nil) when no
// prior exists OR when the only prior is in SUPERSEDED state.
func (s *Service) lookupPriorOperation(ctx context.Context, scope domainops.Scope, idempotencyKey string) (*domainops.Operation, error) {
	prior, err := s.ops.GetLatestForKey(ctx, scope, idempotencyKey, nil)
	if err != nil {
		return nil, err
	}
	if prior != nil && prior.State == domainops.StateSuperseded {
		return nil, nil
	}
	return prior, nil
}

// decideReplayOrFresh classifies the post-lookup state into one
// of the canonical Submit outcomes WITHOUT opening a
// transaction. Returns:
//
//   - hit (idempotency): (SubmitResult with IsIdempotencyHit=true,
//     mustPersist=false, err=nil). The SubmitResult's Job field
//     carries the canonical live Job state (post-worker
//     UPDATEd) via JobGetter.Get OUTSIDE the existing SQL TX.
//
//   - conflict (idempotency): (nil, false, WrapIdempotencyConflict
//     err). NO DB write — caller surfaces the typed sentinel.
//
//   - fresh or force_refresh supersede: (nil, true, nil) — the
//     caller MUST call persistSubmit (transaction.go) which
//     owns the *sql.Tx boundary.
//
//   - self-supersede reference (Push 2.2a MEDIUM code-review fix):
//     caller pre-supplies req.OperationID matching prior would
//     surface as ErrSelfSupersedeReference from the repository's
//     validateForWrite AFTER BeginTx. Detected and surfaced here so
//     the typed error arrives at the input boundary before any
//     TX is opened.
//
// The typed "idempotency hit" log is emitted here (godlike/10
// decision locality: the info-level log is co-located with the
// classification that triggers it).
func (s *Service) decideReplayOrFresh(ctx context.Context, prior *domainops.Operation, req SubmitRequest) (*SubmitResult, bool, error) {
	// Push 2.2a MEDIUM code-review fix (pre-tx guard): a caller
	// pre-supplying req.OperationID == prior.OperationID AND
	// force_refresh=true would surface as ErrSelfSupersedeReference
	// from the repository's validateForWrite AFTER BeginTx. The
	// TX would roll back cleanly, but the error arrives mid-flow
	// instead of at the input boundary. Detect here.
	if prior != nil && prior.RequestHash != req.RequestHash && req.OperationID != "" && req.OperationID == prior.OperationID {
		return nil, false, fmt.Errorf("%w: operation_id=%q",
			domainops.ErrSelfSupersedeReference, req.OperationID)
	}

	switch {
	case prior == nil:
		// No prior — fresh submission. Falls through to TX body.
		return nil, true, nil
	case prior.RequestHash == req.RequestHash:
		// Idempotency hit — same key and same request hash.
		// A retry with the same idempotency key is a replay even when
		// the original payload requested force_refresh. force_refresh
		// controls creation of a fresh operation only when the request
		// identity differs; it must never defeat the idempotency key.
		//
		// FASE 2 close-out: read canonical live Job state via
		// JobGetter so the HTTP layer surfaces the canonical
		// post-terminal Job.Status on replay (no longer a stale
		// QUEUED snapshot from the prior operation row). A
		// JobGetter failure is treated as advisory (typed warn
		// logged; SubmitResult.Job is nil; caller proceeds with
		// the Operation only — the canonical job snapshot is
		// still available via GET /api/jobs/{id}/full).
		s.log.Info("operations.Submit: idempotency hit",
			zap.String("operation_id", prior.OperationID),
			zap.String("scope", string(req.Scope)),
			zap.String("idempotency_key", req.IdempotencyKey),
		)
		var canonicalJob *job.Job
		canonicalJob, jobErr := s.jobGetter.Get(ctx, prior.JobID)
		if jobErr != nil {
			s.log.Warn("operations.Submit: canonical job lookup failed on replay",
				zap.String("operation_id", prior.OperationID),
				zap.String("job_id", prior.JobID),
				zap.Error(jobErr),
			)
		}
		return &SubmitResult{
			Operation:        prior,
			Job:              canonicalJob,
			IsIdempotencyHit: true,
		}, false, nil
	case !req.ForceRefresh && prior.RequestHash != req.RequestHash:
		// 409 IDEMPOTENCY_CONFLICT — same key, different hash.
		// No DB write. Caller (HTTP layer) maps to 409.
		return nil, false, domainops.WrapIdempotencyConflict(
			req.Scope, req.IdempotencyKey, prior.RequestHash, req.RequestHash)
	case req.ForceRefresh:
		// Force-refresh on a prior op — falls through to TX body
		// with supersedes_operation_id set + UPDATE prior to
		// SUPERSEDED in the same atomic TX (transaction.go
		// perform the flip).
		return nil, true, nil
	default:
		// Defensive: unreachable (all cases covered).
		return nil, false, fmt.Errorf("operations.Submit: unreachable state (prior=%+v, force_refresh=%v)", prior, req.ForceRefresh)
	}
}
