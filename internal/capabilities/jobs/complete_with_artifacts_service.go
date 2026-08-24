// Package completion — complete_with_artifacts_service.go
// (P1 wave Azione 6, July 2026).
//
// Sender-side atomic CompleteWithArtifacts orchestrator. Sits
// alongside the P0 Commit 7 CompleteJobService and adds the
// asset-location write step (asset_locations) to the canonical
// 5-step single-TX atomic chain:
//
//  1. UpdateJobToSucceededCAS (job flip)
//  2. InsertResultOnConflict   (job_results row)
//  3. PersistArtifactMap       (job_artifacts mapping)
//  4. InsertOutboxEnvelope     (downstream-event receipts)
//  5. InsertAssetLocations     (asset_locations rows; NEW for Azione 6)
//
// godlike/06 SSOT: this service is the single canonical owner of
// "completed a job with published assets". No other code path
// may mutate jobs.status from non-SUCCEEDED -> SUCCEEDED for
// terminal-completion-with-artifacts purposes. The P0 Commit 7
// CompleteJobService remains canonical for the artifact-free
// path; this service is the artifact-AWARE path.
//
// godlike/07 typed-error contract: every failure path returns a
// typed sentinel reachable via errors.Is (see domain/remote). The
// chained calls (CAS, InsertResult, PersistArtifact, Outbox, now
// AssetLocations) share the same SQLite TX — any partial failure
// rolls back the entire batch.
//
// Migration sequence: EXPAND (this commit, service live in
// parallel with the legacy MarkCompleted path) → BACKFILL
// (forward-pointer: migrate callers from MarkCompleted to
// Service.CompleteWithArtifacts) → CUTOVER (retire
// MarkCompleted for artifact jobs) → CONTRACT (final deprecation
// removal). The legacy MarkCompleted remains canonical for
// artifact-free / capability-agnostic paths (a narrow surface;
// Cap*N-job counts today).
//
// Compile-time pins (Pattern 0):
//   - var _ CompleteJobTxRunner (existing pattern, unchanged)
//   - var _ IdempotencyCachePort (existing pattern, unchanged)
//   - var _ TxContext (the extended interface; this file's
//     implicit interface satisfaction enforces drift detection)
package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Service struct ─────────────────────────────────────────────────

// WithArtifactsService is the Sender-side atomic
// CompleteWithArtifacts orchestrator. Carries the same
// CompleteJobTxRunner + IdempotencyCachePort ports as the P0
// Commit 7 Service — the additional capability (asset_locations
// writes) is delegated to the existing TxRunner's TX boundaries
// (no port extension on the runner; the new work happens INSIDE
// fn's access to an extended TxContext).
//
// godlike/06 SSOT rationale: keeping the runner's surface
// unchanged (no InsertAssetLocations-vs-other-port split)
// preserves the typed-TX-boundary invariant — every in-TX
// operation participates in the same SQLite transaction, with
// runner-level rollback semantics already established by C7.
// The TxContext interface is extended (7th method:
// InsertAssetLocations) so the service can express the new
// operation in-TX without bypassing the existing port surface.
type WithArtifactsService struct {
	rxRunner CompleteJobTxRunner
	cache    IdempotencyCachePort
}

// NewWithArtifactsService is the canonical constructor. Returns
// ErrCompleteWithArtifactsNotConfigured if rxRunner or cache are
// nil (godlike/07 fail-closed posture for half-wired composition,
// mirroring the P0 Commit 7 NewService constructor).
func NewWithArtifactsService(rxRunner CompleteJobTxRunner, cache IdempotencyCachePort) (*WithArtifactsService, error) {
	if rxRunner == nil {
		return nil, fmt.Errorf("%w: rxRunner", remote.ErrCompleteWithArtifactsNotConfigured)
	}
	if cache == nil {
		return nil, fmt.Errorf("%w: cache", remote.ErrCompleteWithArtifactsNotConfigured)
	}
	return &WithArtifactsService{rxRunner: rxRunner, cache: cache}, nil
}

// Compile-time pins (Pattern 0): catastrophic drift between the
// canonical port definitions and the implementation surfaces is a
// build failure, not a runtime panic. The TxContext interface
// now has 7 methods (the new 7th: InsertAssetLocations) — any
// future refactor that drops or renames the method MUST fail to
// compile because the test surface (complete_job_service_test.go +
// complete_with_artifacts_service_test.go) calls it directly via
// the mockTxContext type that satisfies the interface.
//
// (No explicit `var _ TxContext` is declared here: the WithArtifacts
// service uses TxContext as an opaque parameter type, satisfied
// by the runner's fn-injected mockTxContext. The compile-time
// surface that catches drift is the mockTxContext type itself,
// which Go statically binds to the TxContext interface.)

// ── CompleteWithArtifacts entry point ─────────────────────────────

// CompleteWithArtifacts is the canonical Sender-side
// atomic-complete-with-artifacts entry point. The signature
// carries the request envelope as the second positional argument
// and the published-artifacts slice as the third positional
// argument; this matches the user-spec ergonomics
// (signature: `(ctx, *CompleteWithArtifactsRequest, []*PublishedArtifact)`)
// and lets Go's option-pattern spill large/optional data
// (published artifacts) into the tail of the parameter list
// while keeping the small/binding data (worker identity, lease,
// result, asset mappings) on the request envelope.
//
// Mirrors the C7 pattern: idempotent on (jobID, attempt,
// resultHash), fail-closed on every typed-error path,
// no-fake-availability on every wire-shape invariant.
func (s *WithArtifactsService) CompleteWithArtifacts(
	ctx context.Context,
	req *remote.CompleteWithArtifactsRequest,
	published []*finalization.PublishedArtifact,
) (*remote.CompleteWithArtifactsResponse, error) {
	if s == nil {
		return nil, remote.ErrCompleteWithArtifactsNotConfigured
	}
	if req == nil {
		return nil, fmt.Errorf("%w: nil receiver", remote.ErrCompleteWithArtifactsRequestMissingFields)
	}

	// Cross-validate the positional artifact slice against the
	// request's AssetMappings sidecar BEFORE any pre-TX replay
	// probe — every artifact MUST have an AssetMapping (caller
	// is responsible for the catalog lookup upstream of this
	// service).
	artifactIDs := make([]string, 0, len(published))
	for _, pa := range published {
		if pa == nil {
			continue
		}
		artifactIDs = append(artifactIDs, pa.ArtifactID)
	}
	if err := req.ValidateArtifactMappings(artifactIDs); err != nil {
		return nil, err
	}

	// (1) Pre-TX fail-fast gates (godlike/07 no-fake-availability).
	if err := req.Validated(); err != nil {
		return nil, err
	}

	// (2) Pre-TX idempotency replay probe (best-effort cache hit).
	// A cache miss falls through to step 3. The cache key is the
	// canonical (jobID, attempt, result_hash) triple — same as C7
	// — so a partial-success replay against this service and the
	// legacy CompleteJob service short-circuits identically.
	//
	// NOTE: the cache stores a *remote.CompleteJobResponse (the
	// shared C7 cache surface); on a hit we re-derive
	// JobAssetIDs from the request's positional AssetMappings
	// (positional index alignment) since the cached shape only
	// carries JobArtifactIDs.
	if cachedResp, hit, err := s.cache.LookupReplay(ctx, req.JobID, req.Attempt, req.ResultHash); err != nil {
		return nil, fmt.Errorf("complete with artifacts: idempotency cache lookup: %w", err)
	} else if hit && cachedResp != nil {
		return &remote.CompleteWithArtifactsResponse{
			Status:         cachedResp.Status,
			JobArtifactIDs: append([]string(nil), cachedResp.JobArtifactIDs...),
			JobAssetIDs:    deriveJobAssetIDsFromMappings(req, cachedResp.JobArtifactIDs),
			JobID:          cachedResp.JobID,
			Attempt:        cachedResp.Attempt,
			ResultHash:     cachedResp.ResultHash,
		}, nil
	}

	// (3) In-TX orchestration. The runner opens the SQLite TX +
	// invokes fn with the (extended) in-TX port surface. On fn
	// error the runner rolls back; on success the runner commits.
	var (
		outResp   *remote.CompleteWithArtifactsResponse
		errDuring error
	)
	if err := s.rxRunner.RunInTx(ctx, func(txCtx context.Context, tx TxContext) error {
		outResp, errDuring = s.completeWithArtifactsInTx(txCtx, tx, req, published)
		return errDuring
	}); err != nil {
		// If the in-TX fn returned a typed sentinel, surface it
		// WITHOUT the runner wrapping (godlike/06 SSOT: the runner
		// MUST preserve error-chain identity so callers can
		// errors.Is against the typed sentinel).
		//
		// Reused from C7: ErrConcurrentLeaseRefutation +
		// ErrRemoteArtifactHashMismatch +
		// ErrRemoteArtifactSizeMismatch +
		// ErrCompleteJobIdempotencyConflict.
		//
		// New for Azione 6: ErrRemoteArtifactLocationMismatch.
		if errors.Is(err, remote.ErrConcurrentLeaseRefutation) ||
			errors.Is(err, remote.ErrRemoteArtifactHashMismatch) ||
			errors.Is(err, remote.ErrRemoteArtifactSizeMismatch) ||
			errors.Is(err, remote.ErrCompleteJobIdempotencyConflict) ||
			errors.Is(err, remote.ErrRemoteArtifactLocationMismatch) {
			return nil, err
		}
		return nil, fmt.Errorf("complete with artifacts: in-tx orchestration failed: %w", err)
	}

	// (4) Post-TX: persist the canonical response in the
	// idempotency cache so future replays of the same triple
	// can short-circuit at step 2. Cache-write failures are
	// LOGGED but NOT fatal — the SQLite ON CONFLICT dedup remains
	// the authoritative gate (the cache is an optimisation, not
	// the authority).
	_ = s.cache.StoreCanonical(ctx, req.JobID, req.Attempt, req.ResultHash, &remote.CompleteJobResponse{
		Status:         outResp.Status,
		JobArtifactIDs: append([]string(nil), outResp.JobArtifactIDs...),
		JobID:          outResp.JobID,
		Attempt:        outResp.Attempt,
		ResultHash:     outResp.ResultHash,
	})
	return outResp, nil
}

// ── In-TX orchestration ───────────────────────────────────────────

// completeWithArtifactsInTx is the in-TX orchestration body.
// Extracted from CompleteWithArtifacts so the test surface can
// probe the failure paths directly without re-running the pre-TX
// gates.
//
// Atomicity contract: every side-effect (job status flip + result
// row + artifact map + outbox events + asset_locations) commits
// atomically. A failure on ANY side-effect rolls back the entire
// batch — the prior CANONICAL godlike/07 invariant that "no
// half-completed job can exist on disk" extended to cover the
// new 5th step.
func (s *WithArtifactsService) completeWithArtifactsInTx(
	ctx context.Context,
	tx TxContext,
	req *remote.CompleteWithArtifactsRequest,
	published []*finalization.PublishedArtifact,
) (*remote.CompleteWithArtifactsResponse, error) {
	// (3a) Read current job state. The fetch MUST be in-TX so
	// the CAS subsequent to it has a row-locked view (SQLite
	// writes are global; concurrent goroutines cannot race).
	jobRow, err := tx.GetJob(ctx, req.JobID)
	if err != nil {
		return nil, fmt.Errorf("complete with artifacts: fetch job: %w", err)
	}
	if jobRow == nil {
		return nil, fmt.Errorf("%w: jobID=%q not found in TX context",
			remote.ErrConcurrentLeaseRefutation, req.JobID)
	}

	// (3b) Idempotency-on-replay short-circuit (in-TX). If the
	// job is already SUCCEEDED for the same (jobID, attempt),
	// return the cached response without re-doing the CAS.
	if jobRow.Status == job.StatusSucceeded && jobRow.Attempt == req.Attempt {
		prior, ok, err := s.lookupInTxCanonicalResponse(ctx, tx, req)
		if err != nil {
			return nil, fmt.Errorf("complete with artifacts: in-tx replay read: %w", err)
		}
		if ok && prior != nil {
			return &remote.CompleteWithArtifactsResponse{
				Status:         prior.Status,
				JobArtifactIDs: append([]string(nil), prior.JobArtifactIDs...),
				JobAssetIDs:    deriveJobAssetIDsFromMappings(req, prior.JobArtifactIDs),
				JobID:          prior.JobID,
				Attempt:        prior.Attempt,
				ResultHash:     prior.ResultHash,
			}, nil
		}
	}

	// (3c) CAS-update job → SUCCEEDED with the canonical guard
	// (id, lease_id, attempt, status NOT IN terminal sinks).
	rows, err := tx.UpdateJobToSucceededCAS(ctx, req.JobID, req.LeaseID, req.Attempt)
	if err != nil {
		return nil, fmt.Errorf("%w: CAS update failed (jobID=%q, leaseID=%q, attempt=%d): %v",
			remote.ErrConcurrentLeaseRefutation, req.JobID, req.LeaseID, req.Attempt, err)
	}
	if rows == 0 {
		return nil, fmt.Errorf("%w: CAS row-affected=0 (jobID=%q, leaseID=%q, attempt=%d) — lease stolen or attempt drifted",
			remote.ErrConcurrentLeaseRefutation, req.JobID, req.LeaseID, req.Attempt)
	}

	// (3d) ON CONFLICT INSERT into job_results. (Same gate as C7.)
	_, replayed, err := tx.InsertResultOnConflict(
		ctx, req.JobID, req.Attempt,
		codecIDForPayload(req.Result),
		req.Result, req.ResultHash,
	)
	if err != nil {
		if errors.Is(err, remote.ErrCompleteJobIdempotencyConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("complete with artifacts: insert result: %w", err)
	}
	if replayed {
		// ON CONFLICT DO NOTHING preserved an existing row.
		// Re-use the C7 lookupInTxCanonicalResponse helper —
		// mirrors the C7 surface exactly so the artifact-aware
		// variant does not diverge from the artifact-free
		// variant on the in-TX replay semantics.
		prior, ok, lookupErr := s.lookupInTxCanonicalResponse(ctx, tx, req)
		if lookupErr != nil {
			return nil, fmt.Errorf("complete with artifacts: in-tx replay read after ON CONFLICT: %w", lookupErr)
		}
		if ok && prior != nil {
			return &remote.CompleteWithArtifactsResponse{
				Status:         prior.Status,
				JobArtifactIDs: append([]string(nil), prior.JobArtifactIDs...),
				JobAssetIDs:    deriveJobAssetIDsFromMappings(req, prior.JobArtifactIDs),
				JobID:          prior.JobID,
				Attempt:        prior.Attempt,
				ResultHash:     prior.ResultHash,
			}, nil
		}
	}

	// (3e) Hash round-trip check + artifact map persist. The
	// persist goes BEFORE the typed-error-returned-from-hash-
	// mismatch so the canonical godlike/07 typed data (the
	// {drift_summary} from priorHashes) is included in the
	// response message. Same shape as C7 reused unmodified.
	priorHashes, err := tx.GetPriorArtifactHashes(ctx, req.JobID)
	if err != nil {
		return nil, fmt.Errorf("complete with artifacts: fetch prior hashes: %w", err)
	}
	artifactMapEntries := buildArtifactMapEntriesForArtifacts(published)
	artifactIDs, hashMismatchErr := checkArtifactHashRoundTripForArtifacts(published, priorHashes)

	if persistErr := tx.PersistArtifactMap(ctx, req.JobID, req.Attempt, artifactMapEntries); persistErr != nil {
		if errors.Is(persistErr, remote.ErrCompleteJobIdempotencyConflict) {
			return nil, persistErr
		}
		return nil, fmt.Errorf("complete with artifacts: persist artifact map: %w", persistErr)
	}
	if hashMismatchErr != nil {
		return nil, hashMismatchErr
	}

	// (3f) Insert outbox events — one per artifact + one summary
	// event. Mirrors the C7 emit-outbox shape on the
	// artifact-aware type side so the two services emit
	// equivalent event idempotency-key triples for matching
	// (jobID, attempt) replays.
	if outboxErr := s.emitArtifactOutboxEvents(ctx, tx, req, published); outboxErr != nil {
		return nil, fmt.Errorf("complete with artifacts: emit outbox: %w", outboxErr)
	}

	// (3g) Insert asset locations — the new 5th step for Azione 6.
	// Derives AssetLocationEntry slices from the published
	// artifacts + asset_mappings + the typed location surface;
	// delegates the typed write to the (extended) TxContext
	// interface. Called unconditionally (NOT behind an empty-
	// slice guard) so the typed surface is symmetric with
	// PersistArtifactMap — future infra implementations can
	// safely accept zero entries without branching.
	locEntries, locMismatchErr := s.deriveAssetLocationEntries(ctx, tx, req, published)
	if locMismatchErr != nil {
		return nil, fmt.Errorf("complete with artifacts: derive asset locations: %w", locMismatchErr)
	}
	if locErr := tx.InsertAssetLocations(ctx, locEntries); locErr != nil {
		if errors.Is(locErr, remote.ErrRemoteArtifactLocationMismatch) {
			return nil, locErr
		}
		return nil, fmt.Errorf("complete with artifacts: insert asset locations: %w", locErr)
	}

	// (3h) Build the canonical response. JobAssetIDs is the
	// parallel ordered slice to JobArtifactIDs; positional index
	// alignment lets Creator-side correlate jobs and assets at
	// the API surface.
	jobAssetIDs := deriveJobAssetIDsFromMappings(req, artifactIDs)
	return &remote.CompleteWithArtifactsResponse{
		Status:         job.StatusSucceeded,
		JobArtifactIDs: artifactIDs,
		JobAssetIDs:    jobAssetIDs,
		JobID:          req.JobID,
		Attempt:        req.Attempt,
		ResultHash:     req.ResultHash,
	}, nil
}

// ── Helpers extracted to complete_with_artifacts_helpers.go ──
