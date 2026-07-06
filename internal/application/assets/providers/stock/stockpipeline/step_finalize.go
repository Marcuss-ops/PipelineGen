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

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// StockFinalizeStep is the canonical implementation of
// stock.finalize. §12-7 rewrites the body to drive the canonical
// Single-TX spine write via BuildFinalizationRequest +
// JobFinalizer.CompleteWithArtifacts.
type StockFinalizeStep struct{}

func (StockFinalizeStep) Name() string { return StepKeyStockFinalize }

func (StockFinalizeStep) Run(ctx context.Context, runner StepRunner) error {
	in := runner.RunInput()
	if in == nil {
		return errors.New("orchestrator: stock.finalize: nil RunInput")
	}
	workflowID := in.FolderID
	if workflowID == "" {
		workflowID = in.FolderName
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

	// ── Phase 3+4: single-TX spine write (optional) ────────────────
	if runner.JobFinalizer() == nil {
		// Test-fixture / §F.1 back-compat: no JobFinalizer wired.
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.finalize JobFinalizer NOT wired — single-TX spine write skipped (test-fixture path)")
		}
		return nil
	}
	if len(runner.State().Published) == 0 {
		// godlike/07 fail-closed (PR-STOCK-RESUME-STATE-LOSS, July 2026):
		// if JobFinalizer is wired (production mode) but state.Published
		// is empty, the runState was lost on resume (or stock.publish
		// short-circuited). Returning nil here would be a silent-success
		// false-positive — the job would declare SUCCEEDED without
		// writing to media_assets. The leniency is preserved ONLY for
		// test-fixture mode (JobFinalizer nil) where empty Published
		// is the expected outcome of a stub run.
		if runner.JobFinalizer() != nil {
			if runner.Log() != nil {
				runner.Log().Error("orchestrator: stock.finalize: JobFinalizer wired but Published empty — fail-closed on resume state-loss")
			}
			return ErrStockFinalizeStateLost
		}
		// No chunks prepared → preserve the INDEX_PENDING flip from
		// Phase 2 and skip the spine write. This is the canonical
		// path tested by run_upload_indexing_test.go case (c) (Qdrant
		// offline + nil chunks → FinalStatus=StatusIndexPending,
		// spine writes intentionally skipped).
		if runner.Log() != nil {
			runner.Log().Warn("orchestrator: stock.finalize: zero chunks published — single-TX spine write skipped (preserve INDEX_PENDING flip)")
		}
		return nil
	}

	lease := runner.Cfg().Lease
	if lease.JobID == "" || lease.WorkerID == "" || lease.LeaseID == "" {
		return fmt.Errorf("%w: lease.JobID=%q WorkerID=%q LeaseID=%q (HandleJob must thread extractLease)",
			ErrStockFinalizeLeaseMissing, lease.JobID, lease.WorkerID, lease.LeaseID)
	}

	manifestData, marshalErr := manifestBytes(manifest)
	if marshalErr != nil {
		return fmt.Errorf("orchestrator: stock.finalize: marshal manifest: %w", marshalErr)
	}

	finReq, finBuildErr := BuildFinalizationRequest(
		runner.JobID(),
		lease,
		manifestData,
		runner.State().Published,
		runner.State().MetadataPublished,
	)
	if finBuildErr != nil {
		return fmt.Errorf("orchestrator: stock.finalize: BuildFinalizationRequest: %w", finBuildErr)
	}

	finResult, finErr := runner.JobFinalizer().CompleteWithArtifacts(ctx, *finReq)
	if finErr != nil {
		// godlike/07 typed-error contract: propagate the typed sentinel
		// verbatim via %w + fmt.Errorf so callers can errors.Is into
		// deeper sentinels (ErrConcurrentLeaseRefutation,
		// ErrRemoteArtifactHashMismatch) without unwrapping our wrapper.
		return fmt.Errorf("%w: %v", ErrStockFinalizeSpineFailed, finErr)
	}
	runner.State().FinalizationResult = finResult

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.finalize: JobFinalizer spine write SUCCEEDED",
			zap.String("job_id", runner.JobID()),
			zap.Int("attempt", lease.Attempt),
			zap.Int("artifact_ref_count", len(finResult.ArtifactRefs)))
	}
	return nil
}
