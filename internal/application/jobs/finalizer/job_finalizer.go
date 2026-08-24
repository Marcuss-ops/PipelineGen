// Package finalizer provides the concrete implementation of the
// canonical JobFinalizer interface (Spina Dorsale, Fase 2-3, July 2026).
//
// The Finalizer is the SINGLE writer of the terminal SUCCEEDED state.
// Every capability that completes a job MUST route through
// CompleteWithArtifacts — never set SUCCEEDED directly.
//
// Transactional contract (Piano d'Azione § 4.4):
//
//	BEGIN IMMEDIATE
//	  SELECT job (lease check)
//	  check prior terminal result (idempotent completion)
//	  delegate to AssetFinalizerTx.FinalizeAsset (for each artifact)
//	    → UPSERT media_assets
//	    → INSERT asset_versions
//	    → UPSERT asset_locations
//	    → return ArtifactRef + OutboxEvent descriptors
//	  INSERT outbox_events (for each event from AssetFinalizerTx + request)
//	  INSERT job_events
//	  UPDATE jobs SET status = 'SUCCEEDED'
//	COMMIT
//
// All writes happen in ONE transaction. If any step fails, the
// deferred rollback undoes everything.
//
// Idempotency contract (Piano d'Azione § 4.5):
//
//   - Same result hash + same artifacts → idempotent success (no-op).
//   - Different result hash on already-SUCCEEDED job → ErrCompletionConflict.
//   - Stale attempt (request.Attempt < current.Attempt) → ErrStaleAttempt.
//
// File map (godlike/06 SSOT mechanical split, PR-GODOBJ-5-FINALIZER):
//
//   - job_finalizer.go          (this file) — orchestrator + struct + ctor + tx lifecycle
//   - request_validator.go      — validateRequest + buildOptionalArtifactReport (pre-TX)
//   - lease_fence.go            — jobRow + selectJobForFinalization (lease-fenced SELECT)
//   - completion_idempotency.go — handleIdempotentCompletion + fingerprint helpers
//   - artifact_writer.go        — writeArtifacts + writeOutboxEvents (artifact loop)
//   - job_completion_writer.go  — markSucceeded + randomHex (terminal SUCCEEDED flip)
//
// All six files share the *Finalizer receiver so the orchestrator's
// caller-routes (`f.validateRequest()`, `f.selectJobForFinalization(...)`,
// etc.) are byte-equivalent to the pre-split monolithic surface. The
// collapse of the two duplicate `row.status == "SUCCEEDED"` checks in
// the lease-fence SELECT is the single intentional dedup introduced by
// the split (godlike/07 no-fake-availability: both checks routed
// identically to handleIdempotentCompletion — only one is needed).
package finalizer

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/completion"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Finalizer is the concrete implementation of finalization.JobFinalizer.
//
// It holds a *sql.DB (to open transactions), an *outboxevents.Repository
// (to enqueue outbox events), and an *AssetTxFinalizer (to write canonical
// asset records inside the transaction).
//
// Cut 6.5 (July 2026): an optional completion.JobCompletionBus can be
// attached via WithBus. When present, every SUCCESSFUL tx.Commit that
// flips a job to SUCCEEDED publishes a typed JobCompletionEvent so
// API/CLI handlers awaiting job completion can wake up immediately
// instead of polling broker.Get. Nil bus is the conservative default —
// pre-Cut-6.5 callers (and any test that doesn't care about the bus
// surface) see zero behavior change. The bus publish runs OUTSIDE and
// AFTER the SQL transaction (post-commit), so a publish error cannot
// roll back the terminal job flip — the bus is a derived notification
// channel, not part of the SQLite canonical state.
type Finalizer struct {
	db              *sql.DB
	outbox          *outboxevents.Repository
	assetTx         finalization.AssetFinalizerTx
	log             *zap.Logger
	bus             completion.JobCompletionBus
	postCommitHooks interface {
		FirePostCommitHooks(context.Context, finalization.PublishedArtifact)
	}
}

// New creates a Finalizer with the given database, outbox repository,
// and asset-finalizer port (Pattern-0 port abstraction). Production
// callers pass *assetfinalizer.AssetTxFinalizer which satisfies the
// interface.
func New(db *sql.DB, outbox *outboxevents.Repository, assetTx finalization.AssetFinalizerTx, log *zap.Logger) *Finalizer {
	if log == nil {
		log = zap.NewNop()
	}
	f := &Finalizer{
		db:      db,
		outbox:  outbox,
		assetTx: assetTx,
		log:     log,
	}
	if hooks, ok := assetTx.(interface {
		FirePostCommitHooks(context.Context, finalization.PublishedArtifact)
	}); ok {
		f.postCommitHooks = hooks
	}
	return f
}

// WithBus attaches an optional completion.JobCompletionBus to the
// Finalizer. When set, every successful tx.Commit that flips a job
// to SUCCEEDED publishes a typed JobCompletionEvent. When nil, the
// post-commit Publish branch is a no-op (zero behavior change).
//
// Cut 6.5 rationale: the canonical artifact-producing terminal path
// (Script.Generate, ImageGenerate, Books.Process, Lessons.Process,
// Voiceover.* — FASE 3 Spina Dorsale) routes through THIS finalizer.
// A second Publish hook also exists in completion.Service.Complete
// (the new non-artifact backend). The two paths are mutually
// exclusive per job type (FASE 3 routing decision), so the parallel
// hook is zero double-fire risk — see Cut 6.5 commit body.
//
// Return value: the receiver, for fluent composition-root chaining
// (godlike/07 minimum-blast-radius: one-shot setters that don't
// require capturing the result variable).
func (f *Finalizer) WithBus(bus completion.JobCompletionBus) *Finalizer {
	f.bus = bus
	return f
}

// Compile-time assertion: Finalizer implements finalization.JobFinalizer.
var _ finalization.JobFinalizer = (*Finalizer)(nil)

// CompleteWithArtifacts finalises a job atomically with its published
// artifacts. See finalization.JobFinalizer for the full contract.
//
// Orchestrator (steps 1–10, Piano d'Azione § 4.4):
//
//  1. Pre-validation (request_validator.validateRequest).
//  2. Open transaction.
//  3. SELECT job with lease fence (lease_fence.selectJobForFinalization).
//  4. Idempotent completion check — route to
//     completion_idempotency.handleIdempotentCompletion when already SUCCEEDED.
//  5. Build the optional-artifact audit report (request_validator.buildOptionalArtifactReport).
//  6. Adapt *sql.Tx to finalization.Transaction for AssetFinalizerTx.
//  7. Delegate artifact writes to AssetFinalizerTx (artifact_writer.writeArtifacts).
//  8. Write outbox events (artifact_writer.writeOutboxEvents).
//  9. Write result manifest + mark job SUCCEEDED + persist the
//     optional_artifact_report audit sidecar (job_completion_writer.markSucceeded).
//
// 10. Commit.
func (f *Finalizer) CompleteWithArtifacts(
	ctx context.Context,
	req finalization.FinalizationRequest,
) (result *finalization.FinalizationResult, err error) {
	// ── PR-FINALIZER-METRICS (July 2026) ──────────────────────────
	// The named-return parameter `err` is required so the defer can
	// observe the terminal error value (Go semantics: unnamed-return
	// locals are declared at each `return` statement, not at function
	// entry; named returns are in scope from start). The defer
	// increments the canonical FinalizerCompleteArtifactsTotal
	// counter with outcome=ok when err at defer-time is nil;
	// outcome=err when any of the 8 typed-error sentinels fired
	// (validation / begin_tx / lease_fence / idempotent /
	// write_artifacts / write_outbox / mark_succeeded / commit).
	defer func() {
		outcome := "ok"
		if err != nil {
			outcome = "err"
		}
		metrics.FinalizerCompleteArtifactsTotal.WithLabelValues(outcome).Inc()
	}()

	// 1. Pre-validation (outside transaction — fail-fast).
	if validateErr := f.validateRequest(&req); validateErr != nil {
		return nil, validateErr
	}

	// 2. Open transaction.
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("finalizer: begin tx: %w", err)
	}
	defer tx.Rollback()

	// 3. SELECT job with lease fence.
	jobRow, err := f.selectJobForFinalization(ctx, tx, &req.Lease)
	if err != nil {
		return nil, err
	}

	// 4. Check prior terminal result (idempotent completion).
	if jobRow.status == "SUCCEEDED" {
		return f.handleIdempotentCompletion(ctx, jobRow, &req)
	}

	now := time.Now().UTC()

	// 5. Build the optional-artifact audit report (P1.2). Pure
	// function over the request struct — runs BEFORE the SQL
	// operations so a cross-reference mismatch
	// (ErrOptionalArtifactFinalizedMismatch when a declaration
	// promises Finalized but the ArtifactID is missing from
	// Artifacts) fails the request BEFORE any table is touched.
	optionalReport, reportErr := f.buildOptionalArtifactReport(&req, now)
	if reportErr != nil {
		return nil, reportErr
	}

	// 6. Adapt *sql.Tx to finalization.Transaction for AssetFinalizerTx.
	domainTx := assetfinalizer.WrapTx(tx)

	// 7. Delegate artifact writes to AssetFinalizerTx.
	refs, artifactEvents, err := f.writeArtifacts(ctx, domainTx, req.Artifacts)
	if err != nil {
		return nil, err
	}

	// 8. Write outbox events (from request + AssetFinalizerTx).
	// NOTE: capacity hint uses len(req.Artifacts) (the input cardinality),
	// NOT len(artifactEvents) (the AssetFinalizerTx-emitted cardinality).
	// Today's AssetTxFinalizer.FinalizeAsset emits 1 event per artifact,
	// so the two lengths match; using the input cardinality keeps the
	// hint stable across future capabilities that emit variable counts
	// (the slice still grows correctly via append). This matches the
	// pre-split monolithic surface byte-for-byte.
	allEvents := make([]finalization.OutboxEvent, 0, 1+len(req.Events)+len(req.Artifacts))
	// Emit the canonical job.completed outbox event atomically with the
	// SUCCEEDED flip so the outbox pool's performance-projection handler
	// rebuilds performance_runs / performance_steps for every new job.
	allEvents = append(allEvents, finalization.OutboxEvent{
		EventType:   outboxevents.EventJobCompleted,
		AggregateID: req.Result.JobID,
		EventKey:    outboxevents.JobCompletedEventKey(req.Result.JobID),
		Payload:     []byte(`{"job_id":"` + req.Result.JobID + `"}`),
	})
	allEvents = append(allEvents, req.Events...)
	allEvents = append(allEvents, artifactEvents...)
	if err := f.writeOutboxEvents(ctx, tx, allEvents); err != nil {
		return nil, err
	}

	// 9. Write result manifest + mark job SUCCEEDED + persist the
	//    optional_artifact_report audit sidecar inside the same
	//    SQLite transaction.
	if err := f.markSucceeded(ctx, tx, &req, optionalReport); err != nil {
		return nil, err
	}

	// 10. Commit.
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("finalizer: commit: %w", err)
	}
	if f.postCommitHooks != nil && len(req.Artifacts) > 0 {
		// The canonical rows and SUCCEEDED transition are already durable.
		// Transcript/description/summary materialization is an enrichment
		// fan-out and must not hold the worker in post-writer finalization.
		hooks := f.postCommitHooks
		artifacts := append([]finalization.PublishedArtifact(nil), req.Artifacts...)
		go func() {
			for _, artifact := range artifacts {
				hooks.FirePostCommitHooks(context.Background(), artifact)
			}
		}()
	}

	f.log.Info("job finalised with artifacts",
		zap.String("job_id", req.Result.JobID),
		zap.Int("attempt", req.Result.Attempt),
		zap.Int("artifact_count", len(refs)),
		zap.Int("outbox_events", len(allEvents)),
		zap.Int("optional_artifact_count", len(optionalReport)),
	)

	// Cut 6.5 (July 2026): post-commit job-completion notification.
	// The bus fires OUTSIDE and AFTER the SQLite tx.Commit — a
	// publish error cannot roll back the terminal job flip
	// (durable SQLite state is the canonical source of truth; the
	// bus is a derived projection). Nil-bus is zero-op; subscribers
	// always see the FinalizationResult.JobID + Status("SUCCEEDED")
	// when they wake. The handleIdempotentCompletion early-return
	// path is intentionally NOT published here: when the job was
	// already SUCCEEDED on entry, THIS attempt did not write any
	// new SQL state, so there is nothing to signal — the canonical
	// publish point is the tx.Commit that flipped the status.
	//
	// Revision semantics (godlike/07 fail-closed): the event's
	// Revision field is the POST-flip optimistic-concurrency counter,
	// computed as jobRow.revision + 1 — the SQLite UPDATE in step 9
	// (markSucceeded) writes `revision = revision + 1` atomically
	// inside this same tx, so the post-flip value is observable
	// from the row snapshot taken in step 3
	// (selectJobForFinalization). NOT req.Result.Attempt — that
	// conflates the retry counter with the row's revision counter
	// (code-reviewer flagged in part-1 review). When attempt N
	// matches row.revision N (the typical case at first commit), the
	// two would numerically agree; future retry-with-revision-skip
	// scenarios would desync them — Revision on the event payload
	// must reflect the row's actual CC counter, not the attempt
	// counter.
	//
	// godlike/07 defense-in-depth: Publish is wrapped in a deferred
	// recover so a buggy bus implementation cannot panic the
	// worker goroutine AFTER a successful job flip. Durable state
	// stays correct (SQLite committed) but a panic here would leak
	// the worker slot until reconciliation; the recover keeps the
	// publish path best-effort and bounded.
	//
	// godlike/07 observable-fail-closed: a recovered panic leaves a
	// log.Warn trace so the operator can correlate a future
	// stuck-subscriber symptom with a bus-side implementation
	// defect. Durable SQLite state is unchanged regardless, so the
	// warning is observational-only (no rollback / no retry).
	if f.bus != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					f.log.Warn("finalizer: bus publish panic recovered (durable state unchanged)",
						zap.String("job_id", req.Result.JobID),
						zap.Any("panic", r),
					)
					// Forward-pointer: a metrics.FinalizerBusPanicsTotal
					// counter is the natural promotion path; pending
					// infra-layer wiring, the log.Warn is sufficient.
				}
			}()
			f.bus.Publish(completion.JobCompletionEvent{
				JobID:       req.Result.JobID,
				Attempt:     req.Result.Attempt,
				FinalStatus: jobs.StatusSucceeded,
				Err:         nil,
				Revision:    jobRow.revision + 1, // post-flip integer counter
			})
		}()
	}

	return &finalization.FinalizationResult{
		JobID:                  req.Result.JobID,
		Status:                 "SUCCEEDED",
		CompletedAt:            now,
		ArtifactRefs:           refs,
		OptionalArtifactReport: optionalReport,
	}, nil
}
