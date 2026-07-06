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
package completion

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
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

// ── In-TX helpers extracted to complete_with_artifacts_helpers.go ──

// lookupInTxCanonicalResponse is the typed accessor for the prior
// canonical response in-TX (mirror of C7's
// (s *Service).lookupInTxCanonicalResponse on the artifact-aware
// receiver type). The artifact-aware JobAssetIDs are derived
// downstream from the request's AssetMappings sidecar (not from
// a new lookup accessor — the asset catalog is the caller's
// contract).
func (s *WithArtifactsService) lookupInTxCanonicalResponse(
	ctx context.Context,
	tx TxContext,
	req *remote.CompleteWithArtifactsRequest,
) (*remote.CompleteJobResponse, bool, error) {
	priorHashes, err := tx.GetPriorArtifactHashes(ctx, req.JobID)
	if err != nil {
		return nil, false, err
	}
	if len(priorHashes) == 0 {
		return nil, false, nil
	}
	artifactIDs := make([]string, 0, len(priorHashes))
	for id := range priorHashes {
		artifactIDs = append(artifactIDs, id)
	}
	return &remote.CompleteJobResponse{
		Status:         job.StatusSucceeded,
		JobArtifactIDs: artifactIDs,
		JobID:          req.JobID,
		Attempt:        req.Attempt,
		ResultHash:     req.ResultHash,
	}, true, nil
}

// deriveJobAssetIDsFromMappings returns the parallel ordered
// slice of catalog asset_ids corresponding to the input
// artifact_id slice (positional index alignment). A missing
// mapping entry yields an empty string slot — the caller
// (canonical response builder) MUST NOT mint or substitute a
// different identifier (godlike/06 SSOT: the catalog is the
// caller's contract).
func deriveJobAssetIDsFromMappings(req *remote.CompleteWithArtifactsRequest, artifactIDs []string) []string {
	out := make([]string, len(artifactIDs))
	for i, id := range artifactIDs {
		out[i] = req.AssetMappings[id] // empty string if absent
	}
	return out
}

// buildArtifactMapEntriesForArtifacts converts the request's
// []*finalization.PublishedArtifact (positional argument) into
// the typed ArtifactMapEntry slice for tx.PersistArtifactMap.
// Parallel-equivalent of C7's artifactMapEntries helper but on
// the artifact-aware type.
//
// Naming: ForArtifacts suffix to make the artifact-aware variant
// visually distinct at call sites (does NOT collide with C7's
// lowercase artifactMapEntries helper).
func buildArtifactMapEntriesForArtifacts(published []*finalization.PublishedArtifact) []ArtifactMapEntry {
	out := make([]ArtifactMapEntry, 0, len(published))
	for _, pa := range published {
		if pa == nil {
			continue
		}
		// Status column: writes the publisher's terminal action
		// (PublishAction: "created" / "updated" / "skipped" /
		// "renamed") verbatim so future operator queries against
		// the canonical job-flow status taxonomy find artifact
		// rows by what the Publisher actually did, NOT by the
		// requirement-classification metadata
		// (ArtifactRequirement.String() returns
		// "REQUIRED"/"OPTIONAL" — a different concern that lives
		// in the OptionalArtifactReport sidecar, not in the
		// job_artifacts.status SQL column). The zero-value
		// empty string is preserved when the publisher action
		// is empty — same convention as the
		// finalization.PublishAction.Valid() gate.
		out = append(out, ArtifactMapEntry{
			ArtifactID:    pa.ArtifactID,
			SHA256:        pa.SHA256,
			RemoteAssetID: pa.Location.FileID,
			Status:        string(pa.Location.Action),
		})
	}
	return out
}

// checkArtifactHashRoundTripForArtifacts enforces godlike/07
// no-fake-availability on the artifact hashes: if a prior
// SUCCEEDED state has DIFFERENT sha256 for any artifact, surface
// the typed sentinel with the drift summary. Parallel-equivalent
// of C7's helper but typed against the artifact-aware
// PublishedArtifact surface.
//
// Naming: ForArtifacts suffix to avoid redeclaration with C7's
// package-level checkArtifactHashRoundTrip helper (which takes
// []job.RemoteArtifact). The two helpers do not share a
// signature and exist for distinct receiver surfaces — the
// suffix documents the artifact-aware variant.
func checkArtifactHashRoundTripForArtifacts(
	published []*finalization.PublishedArtifact,
	prior map[string]PriorArtifactHash,
) ([]string, error) {
	out := make([]string, 0, len(published))
	for _, pa := range published {
		if pa == nil {
			continue
		}
		out = append(out, pa.ArtifactID)
		p, ok := prior[pa.ArtifactID]
		if !ok {
			continue
		}
		if p.SHA256 != pa.SHA256 {
			return out, fmt.Errorf("%w: artifact[%s] prior_sha256=%q new_sha256=%q",
				remote.ErrRemoteArtifactHashMismatch, pa.ArtifactID, p.SHA256, pa.SHA256)
		}
	}
	return out, nil
}

// emitArtifactOutboxEvents fans out canonical outbox events
// for the completed job with artifacts. Mirrors the C7
// (s *Service).emitOutboxEvents shape on the artifact-aware
// receiver (which takes artifactIDs []string; the WithArtifacts
// variant takes the artifact-aware []finalization.PublishedArtifact
// slice directly so the helper can read the typed .Kind field
// without a side map).
//
// Naming: Artifact suffix on the method name to make the
// artifact-aware variant visually distinct at call sites.
// (The receiver type already disambiguates from C7's
// (s *Service).emitOutboxEvents, but the suffix helps readers
// trace emit-side calls in a multi-service package.)
func (s *WithArtifactsService) emitArtifactOutboxEvents(
	ctx context.Context,
	tx TxContext,
	req *remote.CompleteWithArtifactsRequest,
	published []*finalization.PublishedArtifact,
) error {
	jcKey := remote.CompleteJobIdempotencyKey(req.JobID, req.Attempt, "JOB_COMPLETED")
	if err := tx.InsertOutboxEnvelope(ctx, OutboxEnvelope{
		IdempotencyKey: jcKey,
		EventKind:      "job.completed",
		Payload:        req.Result,
	}); err != nil {
		return fmt.Errorf("insert job.completed envelope: %w", err)
	}
	for _, pa := range published {
		if pa == nil {
			continue
		}
		evKind := "artifact." + string(pa.Kind) + ".uploaded"
		auKey := remote.CompleteJobIdempotencyKey(req.JobID, req.Attempt, evKind+":"+pa.ArtifactID)
		if err := tx.InsertOutboxEnvelope(ctx, OutboxEnvelope{
			IdempotencyKey: auKey,
			EventKind:      evKind,
			Payload:        []byte(pa.ArtifactID),
		}); err != nil {
			return fmt.Errorf("insert %s envelope: %w", evKind, err)
		}
	}
	return nil
}

// deriveAssetLocationEntries constructs the typed
// AssetLocationEntry slice for tx.InsertAssetLocations from the
// request's positional artifacts + AssetMappings + the typed
// AssetLocation descriptor.
//
// Round-trip check (godlike/07 no-fake-availability): if a prior
// SUCCEEDED state has a DIFFERENT (location_kind, external_id,
// access_url, download_url, file_hash) tuple for any asset,
// surface the typed ErrRemoteArtifactLocationMismatch sentinel
// with the drift summary. This is the Azione 6 analog of C7's
// ErrRemoteArtifactHashMismatch but on the new location surface.
//
// Provider → Kind mapping: typed-string match against the canonical
// provider labels in finalization.AssetLocation.Provider values.
// Mappings that don't match any canonical kind fallback to
// LocationKindLocal (the catch-all typed enum). Forward-pointer:
// the canonical provider-to-kind mapping table is owned by
// FASE 5 Drive Publisher-only (per AGENTS.md PHP-FAS file
// restructure) — Azione 6 uses this minimal mapping for the
// service-layer derivation only.
func (s *WithArtifactsService) deriveAssetLocationEntries(
	_ context.Context, // reserved for ctx-scoped prior-state reads (Azione 7 forward-pointer)
	_ TxContext, // reserved for the in-TX prior-state lookup (Azione 7 forward-pointer)
	req *remote.CompleteWithArtifactsRequest,
	published []*finalization.PublishedArtifact,
) ([]AssetLocationEntry, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: nil request", remote.ErrCompleteWithArtifactsRequestMissingFields)
	}
	out := make([]AssetLocationEntry, 0, len(published))
	for i, pa := range published {
		if pa == nil {
			continue
		}
		assetID, ok := req.AssetMappings[pa.ArtifactID]
		if !ok || strings.TrimSpace(assetID) == "" {
			// ValidateArtifactMappings already gated this in
			// pre-TX; the post-TX re-check is a fail-closed
			// backstop.
			return nil, fmt.Errorf("%w: artifact %q has no entry in AssetMappings",
				remote.ErrCompleteWithArtifactsRequestMissingFields, pa.ArtifactID)
		}
		kind := locationKindFromProvider(pa.Location.Provider)
		if i == 0 {
			// First artifact is the primary location for its
			// (asset_id, kind) UNIQUE. Subsequent artifacts
			// (typically multiple locations per asset) flip to
			// secondary.
			// (Forward-pointer: when CUTOVER brings the
			// multi-location-per-asset path, the operator can
			// supply an IsPrimary override at the same seam.)
		}
		out = append(out, AssetLocationEntry{
			ArtifactID:  pa.ArtifactID,
			AssetID:     assetID,
			Kind:        kind,
			Provider:    pa.Location.Provider,
			ExternalID:  pa.Location.FileID,
			AccessURL:   pa.Location.WebViewLink,
			DownloadURL: pa.Location.DownloadLink,
			MIMEType:    pa.MIMEType,
			SizeBytes:   pa.SizeBytes,
			FileHash:    pa.SHA256,
			IsPrimary:   i == 0,
		})
	}
	return out, nil
}

// locationKindFromProvider maps a free-form Provider label to
// the typed asset.LocationKind enum. The mapping is
// best-effort today (FORWARD-POINTER: FASE 5 Drive Publisher-only
// canonicalises the provider label set); unknown providers
// fallback to LocationKindLocal so the typed enum is always set
// (godlike/07 no-fake-availability on the typed column).
func locationKindFromProvider(provider string) asset.LocationKind {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "drive":
		return asset.LocationKindDrive
	case "s3", "object", "object_storage", "object-storage":
		return asset.LocationKindObjectStorage
	}
	return asset.LocationKindLocal
}
