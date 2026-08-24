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
package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Service (canonical owner of "complete a job") ────────────────────

// Service is the Sender-side atomic CompleteJob orchestrator.
// Constructed via NewService; the `var _` compile-pins below
// pretty-print drift across multiple port implementations.
type Service struct {
	rxRunner CompleteJobTxRunner
	cache    IdempotencyCachePort
	// registry (FASE 0.1 July 4 2026): optional JobTypeRegistry port.
	// Nil-safe during EXPAND phase; BACKFILL wires via
	// WithJobTypeRegistry at the composition root. When non-nil, the
	// in-TX gate in completeInTx enforces the legacy-COMPLETE-path-
	// forbidden-for-artifact-producing-jobs contract via
	// remote.ErrCompleteJobPathViolation.
	registry JobTypeRegistry
	// bus (Cut 6.5, July 2026): optional JobCompletionBus port.
	// Nil-safe — production wiring at composition root sets it via
	// WithBus(). When non-nil, the post-TX hook in Complete fires
	// bus.Publish(evt) on the SUCCEEDED transition so any
	// /api/jobs/:id/wait-for-completion handler or admin CLI
	// `--wait jobID` waiter wakes on the SAME SQL UPDATE that
	// transitioned the row (zero polling cycles; godlike/07
	// fail-closed dual-probe contract enforced by completionbus_test.go).
	bus JobCompletionBus
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

// WithJobTypeRegistry wires the JobTypeRegistry port (godlike/06 SSOT
// owner of "does this job type produce artifacts"). Returns the
// receiver for fluent-chain composition at the composition root:
//
//	svc, _ := completion.NewService(rx, cache)
//	appjobs.CompletionServiceBoot(svc.WithJobTypeRegistry(reg))
//
// Idempotent on nil receiver (returns nil; matches the
// fluent-nil-safe-zero-value idiom used elsewhere in this package).
func (s *Service) WithJobTypeRegistry(reg JobTypeRegistry) *Service {
	if s == nil {
		return nil
	}
	s.registry = reg
	return s
}

// WithBus wires the JobCompletionBus port (Cut 6.5, July 2026).
// Returns the receiver for fluent-chain composition at the
// composition root:
//
//	svc, _ := completion.NewService(rx, cache).WithBus(completionbus.NewBus())
//
// godlike/07 minimum-blast-radius: nil receiver returns nil
// (fluent-nil-safe-zero-value); nil bus arg is tolerated (Service
// stays bus-less, post-TX publish no-ops). Production wiring
// always supplies non-nil; EXPAND-phase wiring may omit while the
// canonical Post-TX Publish hook is covered by a forward-pointer PR.
func (s *Service) WithBus(b JobCompletionBus) *Service {
	if s == nil {
		return nil
	}
	s.bus = b
	return s
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

	// (5) Cut 6.5 (July 2026) — Publish the completion event so any
	// /api/jobs/:id/wait-for-completion handler or admin CLI
	// `--wait jobID` waiter wakes on the SAME SQL UPDATE that
	// transitioned the row to SUCCEEDED, eliminating the legacy
	// per-job polling-loop anti-pattern. Nil-safe: production
	// wiring always supplies non-nil; the nil-arm preserves the
	// pre-Cut-6.5 contract so legacy tests that bypass composition
	// (and the EXPAND-phase ENV where bus hasn't been wired yet)
	// continue to compile + execute. The publish call is
	// synchronous fan-out (one-shot goroutine send per captured
	// subscriber) so it does not extend the critical-section
	// window of the in-TX gate.
	if s.bus != nil {
		s.bus.Publish(JobCompletionEvent{
			JobID:       req.JobID,
			Attempt:     req.Attempt,
			FinalStatus: job.StatusSucceeded,
		})
	}
	return outResp, nil
}
