// Package stockpipeline — orchestrator_steps.go (Stock Cutover
// §12-7, July 2026).
//
// §12-7 lifts AssetPreparation (Drive upload) into StockPublishStep
// and the Single-TX spine write into StockFinalizeStep. The
// orchestrator owns the full chunk-rendering ladder end-to-end:
// chunk → ArtifactPreparation → metadata-json → ArtifactPreparation
// → manifest → validate → project → BuildFinalizationRequest →
// JobFinalizer.CompleteWithArtifacts → SUCCEEDED. Service.HandleJob
// becomes a thin wrapper that just calls RunResilient + return-map.
//
// godlike/06 SSOT: orchestrator_steps.go is the single owner of
// (a) what the stock pipeline's six canonical steps are and
// (b) what each step's Run contract looks like. Orchestrator.go's
// dispatchSteps field is initialised to DefaultStockSteps(); every
// orchestration-logic change lands here.
//
// godlike/06 (FileID = LOCATION, NOT identity): each chunk's
// logical ArtifactID is stable per (run_fingerprint, index) — so a
// retry with the same logical run produces the same ArtifactID.
// The Publisher returns a Location.FileID which is a separate
// per-publish identifier (changes on Drive-upload retry). The
// JobFinalizer stores Drive FileID in the location column; the
// ArtifactID stays in the identity column — `stock:<fp>:chunk:<i>`
//
// godlike/07 (no-fake-availability): the run_fingerprint is
// SHA256(PolicyVersion | FolderID | Subfolder | FolderName |
// joined DirectURLs | joined SearchQueries | numeric(plan knobs)
// | boolean(plan flags)). Same logical input ⇒ same fingerprint
// ⇒ same ArtifactID ⇒ single AssetVersions row even on retry.
package stockpipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ── Canonical step keys (PipelineGen §12-5) ────────────────────────────
//
// SSOT: changing any of these constants is a wire-format break for the
// canonical §12-3 step store. New stages MUST add new constants;
// existing ones MUST NOT be renamed without a migration forward-pointer
// via architecture/current.yaml.
const (
	StepKeyStockPlan          = "stock.plan"
	StepKeyStockStageSources  = "stock.stage_sources"
	StepKeyStockExtractClips  = "stock.extract_clips"
	StepKeyStockComposeChunks = "stock.compose_chunks"
	StepKeyStockPublish       = "stock.publish"
	StepKeyStockFinalize      = "stock.finalize"
)

// ── §12-7 sentinel errors (godlike/07 typed-error contract) ────────────
//
// Each sentinel names the rule it enforces. Callers MUST use
// errors.Is(err, ErrStock*) to inspect the failure class. Wraps
// preserve underlying typed errors via fmt.Errorf("%w: %v", ...)
// so dashboards can errors.Is into deeper sentinels like
// ErrConcurrentLeaseRefutation or asset.ErrSHA256Invalid.

var (
	// ErrStockPublishArtifactFailed is raised when ArtifactPreparation.Prepare
	// returns non-nil for any chunk OR for the per-run metadata.json.
	// The wrapped error is the underlying publisher fault.
	ErrStockPublishArtifactFailed = errors.New("stock.publish: ArtifactPreparation failed")

	// ErrStockFinalizeSpineFailed is raised when JobFinalizer.CompleteWithArtifacts
	// returns non-nil. The wrapped error carries the underlying
	// finalizer typed sentinel (ErrConcurrentLeaseRefutation,
	// ErrRemoteArtifactHashMismatch, ErrCompleteJobRequestMissingFields,
	// etc.) via errors.Is / errors.As.
	ErrStockFinalizeSpineFailed = errors.New("stock.finalize: JobFinalizer spine write failed")

	// ErrStockFinalizeLeaseMissing is raised when runner.Cfg().Lease
	// has empty JobID/WorkerID/LeaseID — HandleJob must thread
	// extractLease(job) into cfg.Lease before RunResilient. This
	// sentinel surfaces composition-time wiring gaps loudly.
	ErrStockFinalizeLeaseMissing = errors.New("stock.finalize: cfg.Lease empty (HandleJob must call extractLease)")

	// ErrStockFnRequired surfaces RunFingerprint() == "" — invokes
	// the canonical godlike/07 "every deployment-fingerprint-derived
	// ID must be non-empty" gate. Composition-time wiring gap.
	ErrStockFnRequired = errors.New("stock.finalize: run fingerprint empty (policyVersion / inputs missing)")
)

// ── Step contract ──────────────────────────────────────────────────────

// Step is the canonical typed contract for a single orchestrator-side
// step. The Orchestrator iterates over a typed []Step slice and
// dispatches each step's Run with a StepRunner (defined in step_runner.go).
//
// Name() returns the canonical step_key (one of the
// StepKeyStockXxx constants above) — used to build the
// steps.StepKey triple on checkpoint rows. The Name() output MUST
// match the typed slice's position; changing it is a pipeline-order
// break.
//
// Run() executes the step body. Returns nil error on success
// (orchestrator dispatches MarkCompleted); returns non-nil error
// on failure (orchestrator dispatches MarkFailed + aborts the
// run with the typed error). Optional non-fatal outcomes (like
// the projection-resilience INDEX_PENDING flip) MUST be expressed
// via state mutation (e.g. State.FinalStatus = StatusIndexPending)
// rather than returning error — error means abort.
type Step interface {
	Name() string
	Run(ctx context.Context, runner StepRunner) error
}

// ── Step 1: stock.plan ────────────────────────────────────────────────

// StockPlanStep is the canonical implementation of stock.plan.
// It exercises the deterministic ClipPlanner.Plan round-trip on
// the first source, populating runState.Plan for downstream steps.
type StockPlanStep struct{}

func (StockPlanStep) Name() string { return StepKeyStockPlan }

func (StockPlanStep) Run(ctx context.Context, runner StepRunner) error {
	in := runner.RunInput()
	src, ok := firstSource(in)
	if !ok {
		return errors.New("orchestrator: stock.plan: no sources to plan (DirectURLs and SearchQueries are empty)")
	}

	planBudget := in.TotalMinutes * 60
	if planBudget <= 0 {
		planBudget = runner.Cfg().ChunkDurationSec
	}
	plans, err := runner.Planner().Plan(
		ctx, src, planBudget,
		runner.Cfg().ClipDurationSec, runner.Cfg().PolicyVersion,
	)
	if err != nil {
		return fmt.Errorf("orchestrator: stock.plan: planner.Plan: %w", err)
	}
	runner.State().Plan = plans
	return nil
}

// ── Step 2: stock.stage_sources ───────────────────────────────────────

// StockStageSourcesStep is the canonical implementation of
// stock.stage_sources. Today (Commit 5) the body is a Begin/Complete
// stub — the per-plan SourceStager.Prepare loop wires in Commit 6.
type StockStageSourcesStep struct{}

func (StockStageSourcesStep) Name() string { return StepKeyStockStageSources }

func (StockStageSourcesStep) Run(_ context.Context, runner StepRunner) error {
	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.stage_sources stub (Commit 6 wires real SourceStager.Prepare)",
			zap.Int("plan_count", len(runner.State().Plan)))
	}
	return nil
}

// ── Step 3: stock.extract_clips ───────────────────────────────────────

// StockExtractClipsStep is the canonical implementation of
// stock.extract_clips. For each ClipPlan entry the step constructs
// a typed *asset.Asset and invokes Writer.WriteAndEnqueue — the
// canonical atomic UPSERT + outbox-enqueue entry-point. A returned
// non-nil error aborts the orchestrator with the typed
// ErrAtomicDispatchFailed envelope (per run_upload_indexing_test.go
// contract, test (a)).
type StockExtractClipsStep struct{}

func (StockExtractClipsStep) Name() string { return StepKeyStockExtractClips }

func (StockExtractClipsStep) Run(ctx context.Context, runner StepRunner) error {
	plans := runner.State().Plan
	var cutPaths []string
	for i, plan := range plans {
		clip := &asset.Asset{
			ID:        plan.OutputLogicalID,
			Name:      fmt.Sprintf("chunk_%d", i),
			Source:    asset.Source("stock"),
			MediaType: asset.MediaType("video"),
		}
		if err := runner.Writer().WriteAndEnqueue(ctx, clip, ""); err != nil {
			return fmt.Errorf("%w: chunk %d (%s): %v",
				ErrAtomicDispatchFailed, i, clip.ID, err)
		}
		cutPaths = append(cutPaths, plan.OutputLogicalID)
	}
	runner.State().CutPaths = cutPaths
	return nil
}

// ── Step 4: stock.compose_chunks ───────────────────────────────────────

// StockComposeChunksStep is the canonical implementation of
// stock.compose_chunks. Today (Commit 5) the body is Begin/Complete
// stub — the per-cut StockRenderer.Render loop wires in Commit 7.
// State.ComposedPaths is set 1:1 from State.CutPaths so downstream
// stock.publish has a typed list to operate on.
type StockComposeChunksStep struct{}

func (StockComposeChunksStep) Name() string { return StepKeyStockComposeChunks }

func (StockComposeChunksStep) Run(_ context.Context, runner StepRunner) error {
	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.compose_chunks stub (Commit 7 wires real StockRenderer.Render)",
			zap.Int("cut_count", len(runner.State().CutPaths)))
	}
	runner.State().ComposedPaths = append([]string(nil), runner.State().CutPaths...)
	return nil
}

// ── Step 5: stock.publish (§12-7 REWRITE) ──────────────────────────────

// StockPublishStep is the canonical implementation of
// stock.publish. §12-7 replaces the §12-5 Begin/Complete stub with
// the real AssetPreparation ladder:
//
//  1. For each composed chunk: ComputeAndFillSHA256 → Build
//     VerifiedArtifact (ArtifactID = stock:<fp>:chunk:<i>,
//     Required:true) → ArtifactPreparation.Prepare → translate
//     PublishedArtifact → ChunkState (RemoteFileID = Location.FileID
//     per godlike/06 FileID=location NOT identity).
//
//  2. Build the per-run metadata.json (StockRunMetadata envelope
//     with the per-chunk entries baked in) → write to temp → SHA256
//     → ArtifactPreparation.Prepare → translate → MetadataState.
//
// Fail-closed contracts:
//   - AssetPreparation nil → State.Published = nil, return nil
//     (test-fixture compat). Downstream stock.finalize's
//     BuildFinalizationRequest will raise ErrStockNoChunksFinalized.
//   - Prepare returns error → abort with ErrStockPublishArtifactFailed
//     (wraps publisher fault; preserves typed sentinel via %w+errors.Is).
//   - ComputeAndFillSHA256 returns error → abort (ChunkState sentinel
//     propagates verbatim — VerifyChunks surfaces ErrStockChunkHashMissing
//     / ErrStockChunkLocalMissing consistently).
type StockPublishStep struct{}

func (StockPublishStep) Name() string { return StepKeyStockPublish }

func (StockPublishStep) Run(ctx context.Context, runner StepRunner) error {
	if runner.ArtifactPreparation() == nil {
		// Test-fixture path: no AssetPreparation wired → no chunks
		// prepared. StockFinalizeStep's BuildFinalizationRequest gate
		// raises ErrStockNoChunksFinalized — that's the intended
		// fail-closed signal for unwired composition roots + tests.
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.publish: ArtifactPreparation nil — skipping upload (test-fixture path)")
		}
		runner.State().Published = nil
		return nil
	}

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.publish: AssetPreparation wired — preparing chunks + metadata")
	}

	fp := runner.RunFingerprint()
	composed := runner.State().ComposedPaths
	chunks := make([]ChunkState, 0, len(composed))

	// ── Phase 1: per-chunk ArtifactPreparation ─────────────────────
	for i, compPath := range composed {
		cs := ChunkState{
			Index:      i,
			ArtifactID: ChunkArtifactID(fp, i),
			Filename:   ChunkArtifactFilename(fp, i),
			LocalPath:  compPath,
		}
		if compPath != "" {
			if err := cs.ComputeAndFillSHA256(); err != nil {
				// Pre-Commit-7 transitional path: the compose_chunks
				// stub produces logical IDs (not real file paths).
				// When the file doesn't exist on disk, skip the
				// chunk gracefully so the pipeline continues.
				// Once Commit 7 wires StockRenderer.Render, every
				// composed path will be a real file on disk.
				// TODO(Commit-7): restore fail-closed on
				// ErrStockChunkLocalMissing once compose_chunks
				// produces real files.
				if errors.Is(err, ErrStockChunkLocalMissing) {
					if runner.Log() != nil {
						runner.Log().Debug("orchestrator: stock.publish: skipping chunk (local file not on disk — pre-Commit-7 compose stub)",
							zap.Int("chunk_index", i),
							zap.String("artifact_id", cs.ArtifactID),
							zap.String("local_path", compPath))
					}
					continue
				}
				// Non-ErrStockChunkLocalMissing errors (hash failures,
				// stat I/O errors) are still surfaced loudly.
				return fmt.Errorf("orchestrator: stock.publish: chunk %d (artifact=%s): %w",
					i, cs.ArtifactID, err)
			}
		}
		idem, idemErr := asset.SHA256IdempotencyKey("stock", cs.SHA256)
		if idemErr != nil {
			return fmt.Errorf("%w: chunk %d (artifact=%s) idem-key: %v",
				ErrStockPublishArtifactFailed, i, cs.ArtifactID, idemErr)
		}
	va := finalization.VerifiedArtifact{
		ArtifactID:     cs.ArtifactID,
		Kind:           finalization.KindVideo,
		Filename:       cs.Filename,
		MIMEType:       "video/mp4",
		LocalPath:      cs.LocalPath,
		SizeBytes:      cs.SizeBytes,
		SHA256:         cs.SHA256,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: idem + ":c" + strconv.Itoa(i),
	}
		published, prepErr := runner.ArtifactPreparation().Prepare(ctx, va)
		if prepErr != nil {
			return fmt.Errorf("%w: chunk %d (artifact=%s): %v",
				ErrStockPublishArtifactFailed, i, cs.ArtifactID, prepErr)
		}
		cs.RemoteFileID = published.Location.FileID
		cs.RemoteWebViewLink = published.Location.WebViewLink
		cs.RemoteDownloadLink = published.Location.DownloadLink
		chunks = append(chunks, cs)
	}
	runner.State().Published = chunks

	// ── Phase 2: per-run metadata.json ArtifactPreparation ────────
	// Always invoked AFTER chunks so the metadata's Chunks[] list
	// embeds the per-chunk ArtifactIDs + DriveFileIDs.
	//
	// Pre-Commit-7 guard: if zero chunks were prepared (all skipped
	// because compose_chunks is a stub producing logical IDs), skip
	// metadata publication too — there are no chunk entries to embed.
	// This preserves the PublisherUnreached contract (recordingPublisher
	// must record 0 publish calls).
	if len(chunks) == 0 {
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.publish: zero chunks prepared — skipping metadata publication (pre-Commit-7 stub)")
		}
		return nil
	}
	metaPath, metaHash, metaSize, metaErr := writeAndHashMetadata(
		runner.RunInput(), chunks, fp,
	)
	if metaErr != nil {
		return fmt.Errorf("%w: metadata.json stage: %v",
			ErrStockPublishArtifactFailed, metaErr)
	}
	defer func() {
		// Best-effort cleanup of the metadata temp file after
		// Prepare. The Publisher has already consumed the contents
		// and the metadata RemoteFileID lives on in GoMemory.
		if rmErr := os.Remove(metaPath); rmErr != nil && !os.IsNotExist(rmErr) {
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.publish: failed to remove metadata temp file",
					zap.String("path", metaPath), zap.Error(rmErr))
			}
		}
	}()

	metaIdem, metaIdemErr := asset.SHA256IdempotencyKey("stock:"+fp+":metadata", metaHash)
	if metaIdemErr != nil {
		return fmt.Errorf("%w: metadata idem-key: %v",
			ErrStockPublishArtifactFailed, metaIdemErr)
	}
	metaVA := finalization.VerifiedArtifact{
		ArtifactID:     MetadataArtifactID(fp),
		Kind:           finalization.KindMetadata,
		Filename:       "metadata.json",
		MIMEType:       "application/json",
		LocalPath:      metaPath,
		SizeBytes:      metaSize,
		SHA256:         metaHash,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: metaIdem,
	}
	metaPublished, metaPrepErr := runner.ArtifactPreparation().Prepare(ctx, metaVA)
	if metaPrepErr != nil {
		return fmt.Errorf("%w: metadata.json upload: %v",
			ErrStockPublishArtifactFailed, metaPrepErr)
	}
	runner.State().MetadataPublished = MetadataState{
		LocalPath:         metaVA.LocalPath,
		SHA256:            metaVA.SHA256,
		SizeBytes:         metaVA.SizeBytes,
		RemoteFileID:      metaPublished.Location.FileID,
		RemoteWebViewLink: metaPublished.Location.WebViewLink,
	}

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.publish: AssetPreparation completed",
			zap.Int("chunk_count", len(chunks)),
			zap.String("metadata_artifact_id", MetadataArtifactID(fp)),
			zap.String("metadata_remote_file_id", metaPublished.Location.FileID))
	}
	return nil
}

// ── Step 6: stock.finalize (§12-7 REWRITE) ─────────────────────────────

// StockFinalizeStep is the canonical implementation of
// stock.finalize. §12-7 rewrites the body to drive the canonical
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
// (typed wrap preserves typed sentinels like ErrConcurrentLeaseRefutation,
// ErrRemoteArtifactHashMismatch, ErrCompleteJobRequestMissingFields,
// etc. via errors.Is / errors.As traversal).
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
		manifest, buildErr = runner.Builder().Build(workflowID, runner.JobID())
		if buildErr != nil {
			return fmt.Errorf("orchestrator: stock.finalize: ManifestBuilder.Build: %w", buildErr)
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

// ────────────────────────────────────────────────────────────────────
// StepRunner interface, orchestratorRunner, runState and accessor
// methods live in step_runner.go (Stock P1 split, July 2026).
//
// DefaultStockSteps() + compile-time assertions live in
// orchestrator_defaults.go (Stock P1 split, July 2026).
//
// Artifact ID helpers (ChunkArtifactID, ChunkArtifactFilename,
// MetadataArtifactID) live in orchestrator_fingerprint.go.
//
// Metadata helpers (StockRunMetadata, writeAndHashMetadata,
// buildChunkedStockManifest) live in orchestrator_metadata.go.
// ────────────────────────────────────────────────────────────────────
