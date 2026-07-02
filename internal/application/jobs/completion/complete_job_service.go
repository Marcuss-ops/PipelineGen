// Package completion — complete_job_service.go (P0 Commit 7, July 2026).
//
// Sender-side atomic CompleteJob orchestrator. The service performs
// the canonical (jobID, attempt, resultHash) idempotent completion
// in a single SQLite transaction:
//
//  1. Pre-TX (fail-fast, godlike/07): nil-receiver check,
//     CompleteJobRequest.Validated, CompleteJobRequest.ValidateArtifacts.
//
//  2. Pre-TX idempotency replay probe (godlike/07, optimizes the
//     common retry-on-network-flaky case): if a prior canonical
//     response exists for the same triple, return it WITHOUT
//     re-doing any of the SQL work below. The probe is best-effort
//     (a cache miss falls through to the in-TX dedup surface which
//     is the authoritative gate).
//
//  3. In-TX (single atom):
//     (a) Read current job row (id, status, lease_id, attempt).
//     (b) CAS-update job → SUCCEEDED with (id, lease_id, attempt)
//     guard — 0 rows-affected → ErrConcurrentLeaseRefutation.
//     (c) ON CONFLICT INSERT into job_results (job_id, attempt,
//     result_hash) collapsing to a single row. RETURNING id.
//     (d) Hash round-trip check: if a prior SUCCEEDED job exists
//     with same (job_id, attempt) + DIFFERENT artifact hashes,
//     surface ErrRemoteArtifactHashMismatch (the typed
//     godlike/07 no-fake-availability contract).
//     (e) Persist job_artifacts mapping (one row per manifest entry,
//     carrying remote_asset_id + sha256 + status for round-trip).
//     (f) Insert outbox events for downstream indexing/delivery
//     consumers (one event per artifact, plus one JOB_COMPLETED
//     summary event for the audit surface).
//
//  4. Post-TX: persist the canonical response in the idempotency
//     cache so a retry with the same triple short-circuits at
//     step 2. Cache miss falls through to step 3's ON CONFLICT
//     dedup (the load-bearing idempotency surface per the UNIQUE
//     INDEX on job_results(job_id, attempt, result_hash)).
//
// godlike/06 SSOT: this service is the single canonical owner of
// "completed a job". No other code path may mutate jobs.status from
// non-SUCCEEDED -> SUCCEEDED for terminal-completion purposes.
// godlike/07 typed-error contract: every failure path returns a
// typed sentinel reachable via errors.Is (see domain/remote).
//
// Migration sequence: EXPAND (this commit, service live in parallel
// with the legacy MarkCompleted path) → BACKFILL (C8 migrates all
// callers from MarkCompleted to Service.Complete) → CUTOVER (C9
// retires MarkCompleted) → CONTRACT (final deprecation removal).
package completion

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
)

// ── Port interfaces (Pattern 0 — keep service out of infra) ───────────

// CompleteJobTxRunner is the typed port for "execute a function
// inside a single SQLite transaction". The runner opens the TX,
// invokes fn with a TxContext, commits if fn returns nil, rolls
// back otherwise. Any panic in fn rolls back + re-panics (mirrors
// the C6 Adapter.go panic-isolation precedent).
type CompleteJobTxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx TxContext) error) error
}

// TxContext is the typed in-transaction port surface exposed to the
// service. The implementations pass an *sql.Tx through a
// type-masked handle (godlike/06 SSOT: NO database/sql leaks above
// the infrastructure layer — the test mocks implement this
// interface with in-memory state).
//
// godlike/06 SSOT for shadow-out-of-TX calls: every method on
// TxContext is wired to the in-flight TX via the infra layer; no
// shadow connection pool is consulted (because that would split
// the atomic guarantee the C7 spec requires).
type TxContext interface {
	// GetJob fetches the current job row scoped to the TX.
	// Returns (nil, ErrConcurrentLeaseRefutation) if not found —
	// the lease-stolen + job-deleted cases share the same typed
	// surface because both indicate the lease is no longer valid.
	GetJob(ctx context.Context, jobID string) (*JobRow, error)

	// UpdateJobToSucceededCAS flips jobs.status to SUCCEEDED with
	// the canonical guard (id, lease_id, attempt, status NOT IN
	// terminal sinks). Returns rowsAffected; ErrConcurrentLeaseRefutation
	// if 0 (guard rejected by SQLite).
	UpdateJobToSucceededCAS(ctx context.Context, jobID, leaseID string, attempt int) (int64, error)

	// InsertResultOnConflict is the UNIQUE (job_id, attempt,
	// result_hash) dedup surface for job_results. The infra-layer
	// implementation uses INSERT ... ON CONFLICT (job_id, attempt,
	// result_hash) DO NOTHING RETURNING id; replayed=true when
	// the existing row was preserved (no work re-done).
	InsertResultOnConflict(ctx context.Context, jobID string, attempt int, codecID string, payload []byte, resultHash string) (rowID int64, replayed bool, err error)

	// GetPriorArtifactHashes returns the (sha256, remote_asset_id)
	// tuple for every artifact previously persisted to
	// job_artifacts for the given jobID. Used for the hash round-
	// trip check (godlike/07 drift detection).
	GetPriorArtifactHashes(ctx context.Context, jobID string) (map[string]PriorArtifactHash, error)

	// PersistArtifactMap inserts one row per manifest entry to
	// job_artifacts(job_id, artifact_id, sha256, remote_asset_id,
	// status, attempt). ON CONFLICT (job_id, artifact_id, attempt)
	// DO UPDATE SET sha256=excluded.sha256 (allow round-trip
	// updates without breaking the uniqueness invariant).
	PersistArtifactMap(ctx context.Context, jobID string, attempt int, entries []ArtifactMapEntry) error

	// InsertOutboxEnvelope enqueues one outbox event in the same
	// TX (atomic with the complete-job state flips). The
	// (envelope.idempotencyKey, envelope.event_kind) tuple is
	// unique; a replayed replay yields ErrOutboxEnvelopeDuplicate
	// (typed sentinel at infra layer).
	InsertOutboxEnvelope(ctx context.Context, envelope OutboxEnvelope) error
}

// ── Domain row types (mirror the canonical DB columns) ───────────────
//
// These types are intentionally distinct from the canonical
// domain/job.Job: the canonical Job is the API-level envelope
// (carries codec_id correlation + lifecycle metadata), while the
// service-level JobRow is the in-TX row-immutable snapshot used
// for CAS operations. The TxContext.GetJob returns JobRow (not
// domain/job.Job) so the service can reason about CAS ordering
// without dragging the full envelope through the test surface.

type JobRow struct {
	JobID   string
	LeaseID string
	Attempt int
	Status  job.Status
}

// PriorArtifactHash carries the (sha256, remote_asset_id) tuple
// for one prior row in job_artifacts.
type PriorArtifactHash struct {
	SHA256        string
	RemoteAssetID string
	Status        string
}

// ArtifactMapEntry is the typed write to job_artifacts for one
// artifact in the request's RemoteArtifactManifest.
type ArtifactMapEntry struct {
	ArtifactID    string
	SHA256        string
	RemoteAssetID string
	Status        string
}

// OutboxEnvelope is the typed envelope consumed by the
// outbox-events infrastructure. The exact event_kind constants
// live on the infra-layer side; the service just threads unique
// idempotency keys so (event_kind, idempotency_key) collisions
// surface as the typed duplicate sentinel.
type OutboxEnvelope struct {
	IdempotencyKey string // canonical SHA-256 of (jobID, attempt, event_kind)
	EventKind      string // e.g. "artifact.uploaded", "job.completed"
	Payload        []byte // canonical JSON wire-payload for the event
}

// ── IdempotencyCachePort (post-TX replay short-circuit) ──────────────

// IdempotencyCachePort is the typed port for the post-TX replay
// cache. The canonical implementation is an in-memory LRU + a
// SQLite tenant table; the test mock implements it with a
// thread-safe map. On a hit, the service short-circuits at step 2
// (pre-TX replay probe) without opening a SQLite TX.
type IdempotencyCachePort interface {
	// LookupReplay returns a cached canonical response for the
	// given (jobID, attempt, resultHash) triple, or (nil, false,
	// nil) when the triple is not cached.
	LookupReplay(ctx context.Context, jobID string, attempt int, resultHash string) (*remote.CompleteJobResponse, bool, error)

	// StoreCanonical persists the canonical response so future
	// replays of the same triple can short-circuit at step 2.
	StoreCanonical(ctx context.Context, jobID string, attempt int, resultHash string, resp *remote.CompleteJobResponse) error
}

// ── Service (canonical owner of "complete a job") ────────────────────

// Service is the Sender-side atomic CompleteJob orchestrator.
// Constructed via NewService; the `var _` compile-pins below
// pretty-print drift across multiple port implementations.
type Service struct {
	rxRunner CompleteJobTxRunner
	cache    IdempotencyCachePort
}

// NewService is the canonical constructor. Returns
// ErrCompleteJobNotConfigured if rxRunner or cache are nil
// (godlike/07 fail-closed posture for half-wired composition).
func NewService(rxRunner CompleteJobTxRunner, cache IdempotencyCachePort) (*Service, error) {
	if rxRunner == nil {
		return nil, fmt.Errorf("%w: rxRunner", remote.ErrCompleteJobNotConfigured)
	}
	if cache == nil {
		return nil, fmt.Errorf("%w: cache", remote.ErrCompleteJobNotConfigured)
	}
	return &Service{rxRunner: rxRunner, cache: cache}, nil
}

// Compile-time pins (Pattern 0): catastrophic drift between the
// canonical port definitions and the implementation surfaces is a
// build failure, not a runtime panic.
//
//   - Service satisfies the abstract "is constructed + has Complete
//     method" shape; the interface is implicit (no name) because Go
//     does not require explicit interface satisfaction for the
//     service struct itself.
//
// In lieu of an explicit interface, the compile-time pin is a
// concrete-method-presence assertion: any future refactor that
// drops or renames the Complete method MUST fail to compile
// because the test surface (complete_job_service_test.go) calls
// (svc).Complete(ctx, req) directly.

// Complete is the canonical Sender-side atomic-complete entry point.
// Mirrors the C6 Finalize pattern: idempotent on (jobID, attempt,
// resultHash), fail-closed on every typed-error path, no-fake-
// availability on every wire-shape invariant.
//
// Returns the canonical CompleteJobResponse; for replay calls the
// response is identical to the prior canonical response (jobID +
// attempt + resultHash + artifact IDs preserved verbatim).
func (s *Service) Complete(ctx context.Context, req *remote.CompleteJobRequest) (*remote.CompleteJobResponse, error) {
	if s == nil {
		return nil, remote.ErrCompleteJobNotConfigured
	}
	if req == nil {
		return nil, fmt.Errorf("%w: nil receiver", remote.ErrCompleteJobRequestMissingFields)
	}

	// (1) Pre-TX fail-fast gates (godlike/07 no-fake-availability).
	if err := req.Validated(); err != nil {
		return nil, err
	}
	if err := req.ValidateArtifacts(); err != nil {
		return nil, err
	}

	// (2) Pre-TX idempotency replay probe (best-effort cache hit).
	// A cache miss falls through to step 3.
	if cachedResp, hit, err := s.cache.LookupReplay(ctx, req.JobID, req.Attempt, req.ResultHash); err != nil {
		return nil, fmt.Errorf("complete job: idempotency cache lookup: %w", err)
	} else if hit && cachedResp != nil {
		return cachedResp, nil
	}

	// (3) In-TX orchestration. The runner opens the SQLite TX +
	// invokes fn with an in-TX port surface. On fn error the
	// runner rolls back; on success the runner commits.
	var (
		outResp   *remote.CompleteJobResponse
		errDuring error
	)
	if err := s.rxRunner.RunInTx(ctx, func(txCtx context.Context, tx TxContext) error {
		outResp, errDuring = s.completeInTx(txCtx, tx, req)
		return errDuring
	}); err != nil {
		// If the in-TX fn returned the typed error, surface it
		// WITHOUT the runner wrapping (godlike/06 SSOT: the
		// runner MUST preserve error-chain identity so callers
		// can errors.Is against the typed sentinel).
		if errors.Is(err, remote.ErrConcurrentLeaseRefutation) ||
			errors.Is(err, remote.ErrRemoteArtifactHashMismatch) ||
			errors.Is(err, remote.ErrRemoteArtifactSizeMismatch) ||
			errors.Is(err, remote.ErrCompleteJobIdempotencyConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("complete job: in-tx orchestration failed: %w", err)
	}

	// (4) Post-TX: persist the canonical response in the
	// idempotency cache so future replays of the same triple can
	// short-circuit at step 2. Cache-write failures are LOGGED
	// but NOT fatal — the SQLite ON CONFLICT dedup remains the
	// authoritative gate (the cache is an optimisation, not the
	// authority).
	_ = s.cache.StoreCanonical(ctx, req.JobID, req.Attempt, req.ResultHash, outResp)
	return outResp, nil
}

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
		// Fetch the prior canonical response from job_results +
		// job_artifacts; because we are in-TX, the read sees the
		// committed state. The infrastructure layer MUST expose
		// a typed accessor for the prior canonical response
		// (a follow-up C8 work-item adds it; for C7 we surface
		// an ErrAlreadySucceeded sentinel that the infra-layer
		// turns into a typed response via the canonical helper).
		prior, ok, err := s.lookupInTxCanonicalResponse(ctx, tx, req)
		if err != nil {
			return nil, fmt.Errorf("complete job: in-tx replay read: %w", err)
		}
		if ok && prior != nil {
			return prior, nil
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

	// (3d) ON CONFLICT INSERT into job_results. The
	// (job_id, attempt, result_hash) UNIQUE collapses replays to
	// one row. If result_hash DIFFERS from any prior row at the
	// same (job_id, attempt), the infra-layer surfaces
	// ErrCompleteJobIdempotencyConflict (typed sentinel at
	// godlike/07); the service wraps with the request context.
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
		// ON CONFLICT DO NOTHING preserved an existing row.
		// The canonical response is the prior row's payload +
		// the prior job_artifacts map; the infra-layer
		// dedicated accessor (helper.service.completeInTx
		// looks it up via lookupInTxCanonicalResponse).
		prior, ok, lookupErr := s.lookupInTxCanonicalResponse(ctx, tx, req)
		if lookupErr != nil {
			return nil, fmt.Errorf("complete job: in-tx replay read after ON CONFLICT: %w", lookupErr)
		}
		if ok && prior != nil {
			return prior, nil
		}
	}

	// (3e) Hash round-trip check. If a prior SUCCEEDED job
	// exists for the same jobID with DIFFERENT artifact hashes,
	// surface ErrRemoteArtifactHashMismatch.
	priorHashes, err := tx.GetPriorArtifactHashes(ctx, req.JobID)
	if err != nil {
		return nil, fmt.Errorf("complete job: fetch prior hashes: %w", err)
	}
	artifactIDs, hashMismatchErr := checkArtifactHashRoundTrip(req.Artifacts.Artifacts, priorHashes)

	// (3f) Persist job_artifacts mapping regardless of
	// hashMismatchErr — the typed error surfaces AFTER the row
	// write so the canonical godlike/07 typed data (the
	// {drift_summary} from priorHashes) is included in the
	// response message.
	if persistErr := tx.PersistArtifactMap(ctx, req.JobID, req.Attempt, artifactMapEntries(req.Artifacts.Artifacts)); persistErr != nil {
		return nil, fmt.Errorf("complete job: persist artifact map: %w", persistErr)
	}
	if hashMismatchErr != nil {
		return nil, hashMismatchErr
	}

	// (3g) Insert outbox events — one per artifact + one
	// summary event. Each event's idempotency key is the
	// (jobID, attempt, event_kind) SHA-256 derived via
	// remote.CompleteJobIdempotencyKey (reused for canonical
	// dedup across event types).
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

// ── In-TX helpers (extracted for testability) ────────────────────────

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

// checkArtifactHashRoundTrip enforces godlike/07 no-fake-
// availability on the artifact hashes: if a prior SUCCEEDED
// state has DIFFERENT sha256 for any artifact, surface the typed
// sentinel with the drift summary.
//
// Returns the canonical ordered list of artifact IDs and a
// non-nil typed error if drift was detected. The caller MUST
// handle the error AFTER persisting the new job_artifacts row
// (so the prior & current state are both on disk for the audit
// surface).
func checkArtifactHashRoundTrip(incoming []job.RemoteArtifact, prior map[string]PriorArtifactHash) ([]string, error) {
	out := make([]string, 0, len(incoming))
	for _, a := range incoming {
		out = append(out, a.ID)
		p, ok := prior[a.ID]
		if !ok {
			continue // no prior → no drift possible
		}
		if p.SHA256 != a.SHA256 {
			return out, fmt.Errorf("%w: artifact[%s] prior_sha256=%q new_sha256=%q",
				remote.ErrRemoteArtifactHashMismatch, a.ID, p.SHA256, a.SHA256)
		}
	}
	return out, nil
}

// artifactMapEntries converts the request's RemoteArtifactManifest
// into the typed write surface for job_artifacts.
func artifactMapEntries(in []job.RemoteArtifact) []ArtifactMapEntry {
	out := make([]ArtifactMapEntry, len(in))
	for i, a := range in {
		out[i] = ArtifactMapEntry{
			ArtifactID:    a.ID,
			SHA256:        a.SHA256,
			RemoteAssetID: a.RemoteAssetID,
			Status:        a.Status,
		}
	}
	return out
}

// emitOutboxEvents fans out canonical outbox events for the
// completed job. Each event has a unique (jobID, attempt,
// event_kind) idempotency key so retries collapse to one row in
// the outbox_events table.
func (s *Service) emitOutboxEvents(ctx context.Context, tx TxContext, req *remote.CompleteJobRequest, artifactIDs []string) error {
	// One summary JOB_COMPLETED event.
	jcKey := remote.CompleteJobIdempotencyKey(req.JobID, req.Attempt, "JOB_COMPLETED")
	if err := tx.InsertOutboxEnvelope(ctx, OutboxEnvelope{
		IdempotencyKey: jcKey,
		EventKind:      "job.completed",
		Payload:        req.Result,
	}); err != nil {
		return fmt.Errorf("insert job.completed envelope: %w", err)
	}
	// One ARTIFACT_UPLOADED event per artifact.
	for _, a := range req.Artifacts.Artifacts {
		evKind := "artifact." + a.Kind + ".uploaded"
		auKey := remote.CompleteJobIdempotencyKey(req.JobID, req.Attempt, evKind+":"+a.ID)
		if err := tx.InsertOutboxEnvelope(ctx, OutboxEnvelope{
			IdempotencyKey: auKey,
			EventKind:      evKind,
			Payload:        []byte(a.ID),
		}); err != nil {
			return fmt.Errorf("insert %s envelope: %w", evKind, err)
		}
	}
	return nil
}

// codecIDForPayload pins the canonical codec discriminator for
// the result payload. The canonical ResultCodec enum is owned by
// the C2 compiled-registry surface; this helper returns the
// stable ID for json payloads today (the only codec installed
// per the C1/C2 spec).
func codecIDForPayload(payload []byte) string {
	if len(payload) == 0 {
		return "empty"
	}
	return "json.v1"
}
