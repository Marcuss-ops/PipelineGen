// Package stockpipeline — step_finalize.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026).
//
// SOLE owner of StockFinalizeStep — the canonical implementation
// of the stock.finalize step (Step 6 of the 6-step pipeline) per
// godlike/06 SSOT. §12-7 rewrites the body to drive the canonical
// Single-TX spine write via BuildFinalizationRequest +
// JobFinalizer.CompleteWithArtifacts. The phase ladder shrinks
// (pre-§12-7: build → validate → project; post-§12-7: build →
// validate → project → spine write).
//
// Phase 0 — Fail-closed gate (PR-STOCK-FINALIZE-PHASE-0-GATE, July 2026).
// If JobFinalizer is wired (production mode) BUT state.Published is
// empty, the upstream stock.publish step short-circuited without
// uploading chunks (or the RunState was lost on resume). Returning
// nil here would be a silent-success false-positive — the job would
// declare SUCCEEDED via the broker without writing to media_assets.
// The gate fires BEFORE Phase 1's manifest.Validate so the typed
// sentinel surfaces the upstream state-loss specifically (rather
// than the generic "manifest has zero artifacts" ErrManifestIncomplete
// error which would mask the diagnosis). Test-fixture mode
// (JobFinalizer nil) preserves the compatibility wire-shape
// compat for stock_test fixtures that call Step.Run directly.
//
// Phase 1 — Build + Validate manifest. The manifest is the wire
// artefact (SchemaVersion=1, JobID + per-chunk Artifacts). Build
// path: ManifestBuilder.Build when wired (production) else
// buildChunkedStockManifest fallback. Validate surfaces
// ErrManifestIncomplete (test (b) contract).
//
// Phase 2 — Projection (best-effort). On error flip FinalStatus
// to StatusIndexPending (test (c) contract). DO NOT propagate —
// the spine write still runs in Phase 4 so DB SUCCEEDED is the
// durable state. Per-chunk Qdrant indexing is async via the
// reconciler-driven Qdrant index task.
//
// Phase 3 — BuildFinalizationRequest. Canonical helper from
// finalizer_gates.go. Composes Lease + Result + Artifacts
// (preserves the §12-1 typed-error contract).
//
// Phase 4 — JobFinalizer.CompleteWithArtifacts. The canonical
// Single-TX spine write (asset + version + location + outbox +
// SUCCEEDED). On error propagate as ErrStockFinalizeSpineFailed
// (typed wrap preserves typed sentinels like
// ErrConcurrentLeaseRefutation, ErrRemoteArtifactHashMismatch,
// ErrCompleteJobRequestMissingFields, etc. via errors.Is /
// errors.As traversal).
package stockpipeline

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// StockFinalizeStep is the canonical implementation of
// stock.finalize. §12-7 rewrites the body to drive the canonical
// Single-TX spine write via BuildFinalizationRequest +
// JobFinalizer.CompleteWithArtifacts.
type StockFinalizeStep struct{}

func (StockFinalizeStep) Name() string { return StepKeyStockFinalize }

func (StockFinalizeStep) Run(ctx context.Context, runner StepRunner) error {
	if runner.Log() != nil {
		finalizerWired := "no"
		if runner.JobFinalizer() != nil {
			finalizerWired = "yes"
		}
		runner.Log().Info("orchestrator: stock.finalize: starting",
			zap.Int("published_count", len(runner.State().Published)),
			zap.String("job_finalizer_wired", finalizerWired))
	}

	in := runner.RunInput()
	if in == nil {
		return errors.New("orchestrator: stock.finalize: nil RunInput")
	}
	workflowID := in.FolderID
	if workflowID == "" {
		workflowID = in.FolderName
	}

	// ── Phase 0: fail-closed on state-loss (production mode only) ──
	// godlike/07 NO-FAKE-AVAILABILITY: if the spine writer is wired
	// but state.Published is empty, the upstream publish step did not
	// upload anything — declaring SUCCEEDED here would be a silent-
	// success false-positive. The gate is bypassed in test-fixture mode
	// (JobFinalizer nil) so existing stock_test fixtures remain green.
	if runner.JobFinalizer() != nil && len(runner.State().Published) == 0 {
		if runner.Log() != nil {
			runner.Log().Error("orchestrator: stock.finalize: JobFinalizer wired but Published empty — fail-closed on upstream publish state-loss")
		}
		return ErrStockFinalizeStateLost
	}

	// ── Phase 1: Build + Validate manifest ─────────────────────────
	fp := runner.RunFingerprint()
	if fp == "" {
		return ErrStockFnRequired
	}

	var manifest *job.ArtifactManifest
	var buildErr error
	if runner.Builder() != nil {
		if _, isDefault := runner.Builder().(stockManifestBuilder); isDefault {
			manifest = buildChunkedStockManifest(
				workflowID, runner.JobID(), fp,
				runner.State().Published, runner.State().MetadataPublished,
			)
		} else {
			manifest, buildErr = runner.Builder().Build(workflowID, runner.JobID())
			if buildErr != nil {
				return fmt.Errorf("orchestrator: stock.finalize: ManifestBuilder.Build: %w", buildErr)
			}
		}
	} else {
		manifest = buildChunkedStockManifest(
			workflowID, runner.JobID(), fp,
			runner.State().Published, runner.State().MetadataPublished,
		)
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrManifestIncomplete, err)
	}
	runner.State().Manifest = manifest

	// ── Phase 2: Projection (best-effort, never aborts) ────────────
	runner.State().FinalStatus = job.StatusSucceeded
	if runner.Projection() != nil && manifest != nil {
		if projErr := runner.Projection().Project(ctx, manifest); projErr != nil {
			runner.State().FinalStatus = job.StatusIndexPending
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.finalize projection failed — flipped FinalStatus to StatusIndexPending",
					zap.Error(projErr))
			}
		}
	}

	// Validate the run-completeness invariant before the single-TX spine
	// write. CompleteWithArtifacts is the terminal SUCCEEDED transition;
	// allowing it to run first would leave a job SUCCEEDED when the
	// orchestrator discovers incomplete source/clip accounting afterwards.
	counts := deriveRunCounts(in, runner.State())
	if runner.State().FinalStatus == job.StatusSucceeded &&
		(counts.PlannedClipCount > 0 || counts.SelectedVideoCount > 0) {
		if err := ValidateRunCounts(counts); err != nil {
			return fmt.Errorf("stock run cannot be SUCCEEDED: %w", err)
		}
	}

	// ── Phase 3+4: single-TX spine write ────────────────────────────
	// godlike/07 fail-closed (no-fake-availability): the production
	// symmetric gate (validateStockSymmetricGate in
	// build_bundles_stock.go) forbids a job that reaches stock.finalize
	// with JobFinalizer nil; if the gate was somehow bypassed (drift,
	// test misconfiguration) the step body MUST surface the wiring gap
	// instead of returning nil and letting the broker declare SUCCEEDED.
	// The pre-Commit-A "test-fixture mode silent-success short-circuit"
	// hid this class of wiring gap from production. Test fixtures MUST
	// wire a stubJobFinalizer{} to satisfy the StepRunner interface
	// contract — there is no longer a back-compat skip path.
	if runner.JobFinalizer() == nil {
		// Wrap carries the where-when + remediation hint; the sentinel
		// itself stays terse so callers can errors.Is(err, ErrFinalizerAbsent)
		// without parsing a long human-readable string. Mirrors the file's
		// other branches (ErrStockFinalizeLeaseMissing wraps with field
		// values; ErrStockFinalizeSpineFailed wraps the inner fault).
		return fmt.Errorf("%w: Step.Run reached Phase 3+4 without a wired JobFinalizer (call WithJobFinalizer before RunResilient)", ErrFinalizerAbsent)
	}

	// At this point: JobFinalizer is wired (production mode) AND
	// state.Published is non-empty (Phase-0 gate would have fired
	// otherwise). The spine write proceeds.
	lease := runner.Cfg().Lease
	if lease.JobID == "" || lease.WorkerID == "" || lease.LeaseID == "" {
		return fmt.Errorf("%w: lease.JobID=%q WorkerID=%q LeaseID=%q (HandleJob must thread extractLease)",
			ErrStockFinalizeLeaseMissing, lease.JobID, lease.WorkerID, lease.LeaseID)
	}

	manifestData, marshalErr := manifestBytes(manifest)
	if marshalErr != nil {
		return fmt.Errorf("orchestrator: stock.finalize: marshal manifest: %w", marshalErr)
	}

	finisher := &stockFinalizeAdapter{finalizer: runner.JobFinalizer()}
	finResult, finErr := finisher.Complete(ctx, newStockFinalizeRequest(
		runner.JobID(),
		lease,
		manifestData,
		runner.State().Published,
		runner.State().MetadataPublished,
		fp,
	))
	if finErr != nil {
		// godlike/07 typed-error contract: propagate the typed sentinel
		// verbatim via %w + fmt.Errorf so callers can errors.Is into
		// deeper sentinels (ErrConcurrentLeaseRefutation,
		// ErrRemoteArtifactHashMismatch) without unwrapping our wrapper.
		return fmt.Errorf("%w: %v", ErrStockFinalizeSpineFailed, finErr)
	}
	runner.State().FinalizationResult = finisher.legacyFinalizationResult()

	// Phase 5: durable batch state flip from PUBLISHED → VERIFIED and
	// group/batch status to SUCCEEDED. This happens only after the
	// single-TX spine write succeeded so VERIFIED truly means the
	// artifact is durably committed.
	if batchRepo := runner.BatchRepository(); batchRepo != nil {
		verifiedByGroup := make(map[string]int)
		verifiedCount := 0
		for _, chunk := range runner.State().Published {
			if chunk.SourceURL == "" {
				continue
			}
			groupID := StockArtifactGroupID(runner.JobID(), chunk.SourceURL)
			artifactID := StockArtifactID(runner.JobID(), chunk.SourceURL, chunk.Index)
			if err := batchRepo.MarkArtifactVerified(ctx, artifactID); err != nil {
				if runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.finalize: MarkArtifactVerified failed",
						zap.String("artifact_id", artifactID),
						zap.Error(err))
				}
			} else {
				verifiedByGroup[groupID]++
				verifiedCount++
			}
		}
		for groupID, count := range verifiedByGroup {
			if count == 0 {
				continue
			}
			if err := batchRepo.MarkGroupSucceeded(ctx, groupID, count); err != nil {
				if runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.finalize: MarkGroupSucceeded failed",
						zap.String("group_id", groupID),
						zap.Int("count", count),
						zap.Error(err))
				}
			}
		}
		if err := batchRepo.MarkBatchSucceeded(ctx, runner.JobID(), verifiedCount); err != nil {
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.finalize: MarkBatchSucceeded failed",
					zap.String("batch_id", runner.JobID()),
					zap.Int("count", verifiedCount),
					zap.Error(err))
			}
		}
	}

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.finalize: SUCCEEDED (spine write)",
			zap.String("job_id", runner.JobID()),
			zap.Int("attempt", lease.Attempt),
			zap.Int("artifact_ref_count", len(finResult.ArtifactRefs)))
	}
	return nil
}
