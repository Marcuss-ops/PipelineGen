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
	"os"
	"time"

	"go.uber.org/zap"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// Finalizer is the concrete implementation of finalization.JobFinalizer.
//
// It holds a *sql.DB (to open transactions), an *outboxevents.Repository
// (to enqueue outbox events), and an *AssetTxFinalizer (to write canonical
// asset records inside the transaction).
type Finalizer struct {
	db      *sql.DB
	outbox  *outboxevents.Repository
	assetTx finalization.AssetFinalizerTx
	log     *zap.Logger
}

// New creates a Finalizer with the given database, outbox repository,
// and asset-finalizer port (Pattern-0 port abstraction). Production
// callers pass *assetfinalizer.AssetTxFinalizer which satisfies the
// interface.
func New(db *sql.DB, outbox *outboxevents.Repository, assetTx finalization.AssetFinalizerTx, log *zap.Logger) *Finalizer {
	if log == nil {
		log = zap.NewNop()
	}
	return &Finalizer{
		db:      db,
		outbox:  outbox,
		assetTx: assetTx,
		log:     log,
	}
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
	// ── PR-FINALIZER-SPINE-DEBUG-LOG (July 2026) ───────────────────
	// Diagnostic instrumentation: surface whether the spine is being
	// CALLED by callers (per-step stub bypass, downstream silent-
	// success class) + whether the artifacts slice is non-empty
	// (the chunkless / metadata-only silent-success class). stderr
	// forward-print guarantees operator visibility regardless of log
	// level; the parallel zap.Info keeps the message in the
	// structured log stream for scanner correlation. The named
	// return parameter `err` is required so the defer closure can
	// observe the final return tuple's error value (Go semantics:
	// unnamed-return locals are declared at each `return` statement,
	// not at function entry; named returns are in scope from start).
	fmt.Fprintf(os.Stderr, "[finalizer][debug] CompleteWithArtifacts phase=enter job_id=%s attempt=%d artifacts=%d\n",
		req.Result.JobID, req.Result.Attempt, len(req.Artifacts))
	f.log.Info("finalizer: CompleteWithArtifacts enter",
		zap.String("job_id", req.Result.JobID),
		zap.Int("attempt", req.Result.Attempt),
		zap.Int("artifacts_count", len(req.Artifacts)),
		zap.String("phase", "enter"),
	)

	// Defer captures `err` from the named-return parameter. flag is
	// set to OK on the closing return of the function (default) OR to
	// the explicit error class when err != nil. The outcome flag is
	// the canonical godlike/07 typed-error classification:
	// SPINE_WRITE_OK when no error; SPINE_WRITE_ERR when any of the
	// 8 typed-error sentinels fires (validation / begin_tx /
	// lease_fence / idempotent / write_artifacts / write_outbox /
	// mark_succeeded / commit).
	defer func() {
		flag := "SPINE_WRITE_OK"
		if err != nil {
			flag = "SPINE_WRITE_ERR"
		}
		fmt.Fprintf(os.Stderr, "[finalizer][debug] CompleteWithArtifacts phase=exit job_id=%s attempt=%d artifacts=%d spine_write_outcome=%s err=%v\n",
			req.Result.JobID, req.Result.Attempt, len(req.Artifacts), flag, err)
		f.log.Info("finalizer: CompleteWithArtifacts exit",
			zap.String("job_id", req.Result.JobID),
			zap.Int("attempt", req.Result.Attempt),
			zap.Int("artifacts_count", len(req.Artifacts)),
			zap.String("phase", "exit"),
			zap.String("spine_write_outcome", flag),
			zap.Error(err),
		)
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
	allEvents := make([]finalization.OutboxEvent, 0, len(req.Events)+len(req.Artifacts))
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

	f.log.Info("job finalised with artifacts",
		zap.String("job_id", req.Result.JobID),
		zap.Int("attempt", req.Result.Attempt),
		zap.Int("artifact_count", len(refs)),
		zap.Int("outbox_events", len(allEvents)),
		zap.Int("optional_artifact_count", len(optionalReport)),
	)

	return &finalization.FinalizationResult{
		JobID:                  req.Result.JobID,
		Status:                 "SUCCEEDED",
		CompletedAt:            now,
		ArtifactRefs:           refs,
		OptionalArtifactReport: optionalReport,
	}, nil
}
