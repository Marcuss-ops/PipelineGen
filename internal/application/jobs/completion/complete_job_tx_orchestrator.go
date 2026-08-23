// Package completion — complete_job_tx_orchestrator.go: in-TX orchestration body.
//
// 2026-07-06 (Phase 4 decomposition): extracted from complete_job_service.go
// per the god-object decomposition plan. completeInTx is the canonical
// in-TX body that Complete() delegates to via rxRunner.RunInTx.
//
// Atomicity contract (godlike/07): every side-effect (job status flip +
// result row + artifact map + outbox events) commits atomically. A failure
// on ANY side-effect rolls back the entire batch — no half-completed job
// can exist on disk.
//
// lookupInTxCanonicalResponse is a C7 transitional helper; C8 replaces
// it with a dedicated infra-layer canonical reader.
package completion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// completeInTx is the in-TX orchestration body. Extracted from
// Complete so the test surface can probe the failure paths
// directly without re-running the pre-TX gates.
//
// Atomicity contract: every side-effect (job status flip + result
// row + artifact map + outbox events) commits atomically. A failure
// on ANY side-effect rolls back the entire batch — the prior
// CANONICAL godlike/07 invariant that "no half-completed job can
// exist on disk".
func (s *Service) completeInTx(ctx context.Context, tx TxContext, req *remote.CompleteJobRequest) (*remote.CompleteJobResponse, error) {
	// (3a) Read current job state. The fetch MUST be in-TX so
	// the CAS subsequent to it has a row-locked view (SQLite
	// writes are global; concurrent goroutines cannot race).
	jobRow, err := tx.GetJob(ctx, req.JobID)
	if err != nil {
		return nil, fmt.Errorf("complete job: fetch job: %w", err)
	}
	if jobRow == nil {
		return nil, fmt.Errorf("%w: jobID=%q not found in TX context", remote.ErrConcurrentLeaseRefutation, req.JobID)
	}

	// (3b) Idempotency-on-replay short-circuit (in-TX). If the
	// job is already SUCCEEDED for the same (jobID, attempt),
	// return the cached response without re-doing the CAS.
	if jobRow.Status == job.StatusSucceeded && jobRow.Attempt == req.Attempt {
		prior, ok, err := s.lookupInTxCanonicalResponse(ctx, tx, req)
		if err != nil {
			return nil, fmt.Errorf("complete job: in-tx replay read: %w", err)
		}
		if ok && prior != nil {
			return prior, nil
		}
	}

	// (3b-bis) FASE 0.1 (July 4, 2026) in-TX typed-error gate.
	if s.registry != nil && jobRow.Status != job.StatusSucceeded && s.registry.ProducesArtifacts(jobRow.JobType) && len(req.Artifacts.Artifacts) == 0 && !isWaitingChildrenResult(req.Result) {
		return nil, fmt.Errorf("%w: jobType=%q (registry-declared ProducesArtifacts=true; legacy Complete forbidden — use CompleteWithArtifacts)",
			remote.ErrCompleteJobPathViolation, jobRow.JobType)
	}

	// (3c) CAS-update job → SUCCEEDED.
	rows, err := tx.UpdateJobToSucceededCAS(ctx, req.JobID, req.LeaseID, req.Attempt)
	if err != nil {
		return nil, fmt.Errorf("%w: CAS update failed (jobID=%q, leaseID=%q, attempt=%d): %v",
			remote.ErrConcurrentLeaseRefutation, req.JobID, req.LeaseID, req.Attempt, err)
	}
	if rows == 0 {
		return nil, fmt.Errorf("%w: CAS row-affected=0 (jobID=%q, leaseID=%q, attempt=%d) — lease stolen or attempt drifted",
			remote.ErrConcurrentLeaseRefutation, req.JobID, req.LeaseID, req.Attempt)
	}

	// (3d) ON CONFLICT INSERT into job_results.
	_, replayed, err := tx.InsertResultOnConflict(
		ctx, req.JobID, req.Attempt,
		codecIDForPayload(req.Result),
		req.Result, req.ResultHash,
	)
	if err != nil {
		if errors.Is(err, remote.ErrCompleteJobIdempotencyConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("complete job: insert result: %w", err)
	}
	if replayed {
		prior, ok, lookupErr := s.lookupInTxCanonicalResponse(ctx, tx, req)
		if lookupErr != nil {
			return nil, fmt.Errorf("complete job: in-tx replay read after ON CONFLICT: %w", lookupErr)
		}
		if ok && prior != nil {
			return prior, nil
		}
	}

	// (3e) Hash round-trip check.
	priorHashes, err := tx.GetPriorArtifactHashes(ctx, req.JobID)
	if err != nil {
		return nil, fmt.Errorf("complete job: fetch prior hashes: %w", err)
	}
	artifactIDs, hashMismatchErr := checkArtifactHashRoundTrip(req.Artifacts.Artifacts, priorHashes)

	// (3f) Persist job_artifacts mapping regardless of hashMismatchErr.
	if persistErr := tx.PersistArtifactMap(ctx, req.JobID, req.Attempt, artifactMapEntries(req.Artifacts.Artifacts)); persistErr != nil {
		return nil, fmt.Errorf("complete job: persist artifact map: %w", persistErr)
	}
	if hashMismatchErr != nil {
		return nil, hashMismatchErr
	}

	// (3g) Insert outbox events.
	if outboxErr := s.emitOutboxEvents(ctx, tx, req, artifactIDs); outboxErr != nil {
		return nil, fmt.Errorf("complete job: emit outbox: %w", outboxErr)
	}

	// (3h) Build the canonical response.
	return &remote.CompleteJobResponse{
		Status:         job.StatusSucceeded,
		JobArtifactIDs: artifactIDs,
		JobID:          req.JobID,
		Attempt:        req.Attempt,
		ResultHash:     req.ResultHash,
	}, nil
}

// isWaitingChildrenResult identifies the artifactless hand-off result used
// by fan-out parents. The parent keeps the artifact-producing job policy for
// the single-item path, but a batch parent owns no files until its children
// complete; rejecting it here would prevent the aggregator from running.
func isWaitingChildrenResult(raw []byte) bool {
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return false
	}
	state, _ := result["parent_state"].(string)
	return state == "waiting_children"
}

// lookupInTxCanonicalResponse is the typed accessor for the prior
// canonical response in-TX. The infrastructure-layer (follow-up
// C8) implements the canonical accessor; for C7 we provide a
// minimal reconstruction from GetPriorArtifactHashes + a heuristic
// match on the (jobID, attempt, result_hash) row. The infra-layer
// MUST replace this helper with the dedicated canonical reader.
func (s *Service) lookupInTxCanonicalResponse(ctx context.Context, tx TxContext, req *remote.CompleteJobRequest) (*remote.CompleteJobResponse, bool, error) {
	priorHashes, err := tx.GetPriorArtifactHashes(ctx, req.JobID)
	if err != nil {
		return nil, false, err
	}
	if len(priorHashes) == 0 {
		return nil, false, nil
	}
	artifactIDs := make([]string, 0, len(priorHashes))
	for _, a := range req.Artifacts.Artifacts {
		artifactIDs = append(artifactIDs, a.ID)
	}
	return &remote.CompleteJobResponse{
		Status:         job.StatusSucceeded,
		JobArtifactIDs: artifactIDs,
		JobID:          req.JobID,
		Attempt:        req.Attempt,
		ResultHash:     req.ResultHash,
	}, true, nil
}
