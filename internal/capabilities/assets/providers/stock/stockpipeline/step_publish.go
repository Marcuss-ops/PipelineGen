// Package stockpipeline — step_publish.go
// (PR-SPLIT-STEP-PUBLISH, 2026-08-08; PR-STOCK-ORCHESTRATOR-SPLIT, July 2026).
//
// SOLE owner of StockPublishStep — the canonical implementation
// of the stock.publish step (Step 5 of the 6-step pipeline) per
// godlike/06 SSOT. The Run() body is a THIN orchestrator that
// delegates to the 3 sister files (same package, no imports change):
//
//   - step_publish_chunks_phase.go: per-chunk AssetPreparation
//     loop (§12-7 ladder via publishChunkPhase).
//   - step_publish_metadata_phase.go: metadata.json AssetPreparation
//   - TimestampDriveFolderLink backfill (§12-7 step 2 via
//     publishMetadataPhase).
//   - step_publish_naming.go: 10 Drive-side naming helpers
//     (root folder + per-clip leaf + sanitization cascade).
//
// Lookup path StockPublishStep.Run is byte-stable (public API
// unchanged); pre-existing tests in step_publish_test.go pass
// unchanged per godlike/07 minimum-blast-radius.
//
// godlike/07 fail-closed contracts (preserved verbatim from the
// pre-split body):
//   - AssetPreparation nil → State.Published = nil, return nil
//     (test-fixture compat; downstream stock.finalize's
//     BuildFinalizationRequest raises ErrStockNoChunksFinalized).
//   - len(chunks) == 0 + AssetPreparation wired →
//     ErrStockPublishStateLost (fail-closed on resume state-loss:
//     prevents silent-success false-positives in production mode).
//   - Prepare returns error → abort with
//     ErrStockPublishArtifactFailed (publisher fault wrapped
//     via %w + errors.Is).
//   - ComputeAndFillSHA256 returns error → abort (ChunkState
//     sentinel propagates verbatim — VerifyChunks surfaces
//     ErrStockChunkHashMissing / ErrStockChunkLocalMissing).
package stockpipeline

import (
	"context"
	"fmt"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"go.uber.org/zap"
)

// StockPublishStep is the canonical implementation of
// stock.publish. §12-7 replaces the §12-5 Begin/Complete stub
// with the real AssetPreparation ladder (delegated to the
// publishChunkPhase + publishMetadataPhase sister files via
// same-package visibility — no new exported symbols).
type StockPublishStep struct{}

func (StockPublishStep) Name() string { return StepKeyStockPublish }

type publishCutPlan struct {
	ClipIndex int
	Plan      ClipPlan
	Item      CutItemResult
	Artifact  finalization.VerifiedArtifact
}

type publishPlan struct {
	Cuts []publishCutPlan
}

func buildPublishPlan(groupPlans []ClipPlan, result CutBatchResult, batchID, sourceID string, rootFolderName, resolvedFolderID, timestampGroupName string, in *RunInput, segmentCounts map[string]int) (publishPlan, []string, error) {
	plan := publishPlan{}
	paths := make([]string, 0, len(groupPlans))
	for clipIdx, clipPlan := range groupPlans {
		item := result.Items[clipIdx]
		if item.Status == CutItemStatusFailed || item.OutputPath == "" || item.SHA256Hex == "" {
			continue
		}
		leafName := timestampGroupName
		if in != nil && len(in.Clips) > 0 {
			leafName = stockClipFolderName(in, clipPlan, timestampGroupName)
		}
		segmentCounts[leafName]++
		filename := fmt.Sprintf("clip_%03d.mp4", segmentCounts[leafName])
		plan.Cuts = append(plan.Cuts, publishCutPlan{ClipIndex: clipIdx, Plan: clipPlan, Item: item, Artifact: finalization.VerifiedArtifact{
			ArtifactID: clipPlan.OutputLogicalID, Kind: finalization.KindVideo, Filename: filename, MIMEType: "video/mp4",
			LocalPath: item.OutputPath, SizeBytes: item.SizeBytes, SHA256: item.SHA256Hex,
			Requirement: finalization.ArtifactRequirementRequired, IdempotencyKey: clipPlan.OutputLogicalID + ":" + item.SHA256Hex,
			Description: clipPlan.Description, RootFolderName: rootFolderName, ResolvedFolderID: resolvedFolderID,
			RootFolderResolved: in != nil && in.DriveFolderResolved, PathLeafName: leafName,
		}})
		paths = append(paths, item.OutputPath)
	}
	sort.SliceStable(plan.Cuts, func(i, j int) bool { return plan.Cuts[i].ClipIndex < plan.Cuts[j].ClipIndex })
	return plan, paths, nil
}

// Run is the slim orchestrator for stock.publish: derive step
// inputs (root folder + override + leaf name + the explicit-
// timestamps gate) from the runner context, delegate the
// per-chunk loop to publishChunkPhase, fail-closed on resume
// state-loss, then delegate the run-level metadata.json +
// TimestampFolderLink backfill to publishMetadataPhase. Both
// phases return typed errors that bubble to the orchestrator
// unchanged.
//
// godlike/07 minimum-blast-radius: lookup path StockPublishStep.Run
// stays byte-stable (pre-split tests resolve via same-package
// visibility to the renamed factories). No new exported symbols.
// Imports trimmed to just `context` + `go.uber.org/zap` — the 3
// sister files own all other deps (finalization/asset/strings/
// strconv/os/pathutil/slug/domaindelivery/time/net/url).
func (StockPublishStep) Run(ctx context.Context, runner StepRunner) (err error) {
	// Canonical stage: stock.publish owns the Drive publication ladder
	// (per-chunk AssetPreparation + metadata.json + destination reconcile).
	// Without it that wall time fell outside every top-level stage and
	// surfaced as unattributed in the timing breakdown.
	publishMetric := startStockPhase(ctx, runner, StepKeyStockPublish)
	defer func() { finishStockPhase(runner, publishMetric, StepKeyStockPublish, err) }()

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
		runner.Log().Info("orchestrator: stock.publish: starting",
			zap.Int("composed_paths", len(runner.State().ComposedPaths)))
		runner.Log().Info("orchestrator: stock.publish: AssetPreparation wired — preparing chunks + metadata")
	}

	in := runner.RunInput()
	fp := runner.RunFingerprint()
	explicitTimestamps := in != nil && len(in.Clips) > 0
	rootFolderName := stockRootFolderName(in)
	resolvedFolderID := stockResolvedFolderID(in)
	timestampGroupName := stockTimestampGroupName(in)
	explicitTimestampGroupName := stockTimestampParentGroupName(in)
	if explicitTimestamps {
		timestampGroupName = explicitTimestampGroupName
	}

	// Phase 1: per-chunk AssetPreparation ladder (see
	// step_publish_chunks_phase.go). Returns built ChunkState
	// slice (or existingPublished copy when publishedReady).
	chunks, err := publishChunkPhase(ctx, runner, in, fp, explicitTimestamps,
		rootFolderName, resolvedFolderID, timestampGroupName,
		runner.State().Plan, runner.State().ComposedPaths, runner.State().Published)
	if err != nil {
		return err
	}
	runner.State().Published = chunks

	// godlike/07 fail-closed (PR-STOCK-RESUME-STATE-LOSS, July 2026):
	// if AssetPreparation is wired (production mode) but ComposedPaths
	// was empty (zero chunks prepared), the RunState was lost on resume
	// (or compose_chunks short-circuited). Returning nil here would be
	// a silent-success false-positive — the job would declare SUCCEEDED
	// without uploading anything. The leniency is preserved ONLY for
	// test-fixture mode (AssetPreparation nil) handled at the top of
	// Run; production mode (AssetPreparation wired) is fail-closed.
	if len(chunks) == 0 {
		if runner.Log() != nil {
			runner.Log().Error("orchestrator: stock.publish: AssetPreparation wired but ComposedPaths empty — fail-closed on resume state-loss")
		}
		return ErrStockPublishStateLost
	}

	// Phase 2: metadata.json ArtifactPreparation + TimestampFolderLink
	// backfill on every chunk (see step_publish_metadata_phase.go).
	metadataState, err := publishMetadataPhase(ctx, runner, in, fp, explicitTimestamps,
		rootFolderName, resolvedFolderID, timestampGroupName, chunks)
	if err != nil {
		return err
	}
	runner.State().MetadataPublished = metadataState

	// Phase 3: destination hygiene (PR-STOCK-DESTINATION-RECONCILE). The plan
	// that was just published is the only one allowed to live in the folder: any
	// pipeline-owned artifact from an earlier plan (different content, so never
	// reused, so never renamed) is removed here. Non-fatal by design — the
	// artifacts are already durable when this runs.
	reconcilePublishedDestination(ctx, runner, in, chunks)

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.publish: SUCCEEDED",
			zap.Int("chunk_count", len(chunks)),
			zap.String("metadata_artifact_id", MetadataArtifactID(fp)),
			zap.String("metadata_remote_file_id", metadataState.RemoteFileID))
	}
	return nil
}

// Compatibility wrappers keep existing same-package callers stable while the
// pure naming implementation is owned by the publish capability package.
func stockRootFolderName(in *RunInput) string { return RootFolderName(in) }

func stockResolvedFolderID(in *RunInput) string { return ResolvedFolderID(in) }

func stockTimestampGroupName(in *RunInput) string { return TimestampGroupName(in) }

func stockClipFolderName(in *RunInput, plan ClipPlan, fallback string) string {
	return ClipFolderName(in, plan, fallback)
}

func stockTimestampParentGroupName(in *RunInput) string {
	return TimestampParentGroupName(in)
}

func perClipLeafName(plan ClipPlan) string { return PerClipLeafName(plan) }

func slugifyTitle(title string) string { return SlugifyTitle(title) }

// ── Post-publish destination hygiene (PR-STOCK-DESTINATION-RECONCILE) ──
//
// Folded in from destination_reconcile.go + destination_reconcile_port.go
// (2026-09-15): the registered
// internal/capabilities/assets/providers/stock/stockpipeline hotspot MUST NOT
// gain production files (percheck_legacy_hotspot_growth), and this pass is
// Phase 3 of StockPublishStep.Run below — the step that owns it is the right
// file for it.
//
// A Stock run publishes into a resolved Drive folder. Because the publication
// is content-addressed (idempotency key = clipID + sha256) and the Drive
// display name is a per-run ordinal (clip_%03d), re-running the same
// destination with a DIFFERENT plan leaves the artifacts of the previous plan
// behind:
//
//   - the reused files are renamed onto the new plan's names (see
//     drive.Uploader PutFile ConflictSkip+rename), so names stay honest;
//   - the files whose content is NOT in the new plan stay in the folder under
//     their old names, colliding with the new occupants (observed live: 12
//     files, clip_001/clip_003 present twice).
//
// This surface owns the GC half: after a successful publish, every artifact in
// the destination folder that (a) was written by this pipeline — it carries the
// P0.6 `pipelinegen_idempotency_key` appProperty — and (b) is not part of the
// plan that was just published is removed. Operator-uploaded files are never
// touched: without the ownership marker they are invisible to the reconciler.
//
// Failure policy (deliberate, godlike/07 honest-limitation): reconciliation is
// a hygiene pass, NOT part of the artifact contract. A Drive list/trash failure
// is logged at Warn and does NOT fail the job — the published artifacts are
// already durable and correct; only stale siblings survive one extra run.

// DestinationArtifact is one file observed in a run's destination folder.
type DestinationArtifact struct {
	FileID   string
	Name     string
	MimeType string
	// OwnedByPipeline is true when the file carries the pipeline's
	// idempotency-key appProperty, i.e. it was written by a Stock/YouTube
	// publication rather than by an operator.
	OwnedByPipeline bool
}

// DestinationArtifactStore is the narrow Drive-facing seam the reconciler
// needs. Implementations live in the platform layer (drive.Uploader).
type DestinationArtifactStore interface {
	ListArtifacts(ctx context.Context, folderID string) ([]DestinationArtifact, error)
	RemoveArtifact(ctx context.Context, fileID string) error
}

// DestinationReconciler removes the artifacts left by earlier plans in the
// folder the current plan publishes into.
type DestinationReconciler interface {
	// ReconcileDestination lists folderID and removes every pipeline-owned
	// artifact whose FileID is not in keepFileIDs. Returns a report of what it
	// observed and removed.
	ReconcileDestination(ctx context.Context, folderID string, keepFileIDs []string) (StaleArtifactReport, error)
}

// StaleArtifactReport is the audit shape of one reconciliation pass.
type StaleArtifactReport struct {
	FolderID     string
	Scanned      int
	Removed      int
	RemovedNames []string
}

// staleArtifacts is the pure selection rule of the reconciler: pipeline-owned,
// not part of the current plan, and de-duplicated by file ID. Sorting is
// intentionally left to the caller (Drive ordering is not stable); the file IDs
// of the keep set are authoritative.
func staleArtifacts(files []DestinationArtifact, keepFileIDs []string) []DestinationArtifact {
	keep := make(map[string]struct{}, len(keepFileIDs))
	for _, id := range keepFileIDs {
		if id != "" {
			keep[id] = struct{}{}
		}
	}
	out := make([]DestinationArtifact, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, f := range files {
		if f.FileID == "" || !f.OwnedByPipeline {
			continue
		}
		if _, isKept := keep[f.FileID]; isKept {
			continue
		}
		if _, dup := seen[f.FileID]; dup {
			continue
		}
		seen[f.FileID] = struct{}{}
		out = append(out, f)
	}
	return out
}

// ReconcileDestination implements DestinationReconciler on top of the narrow
// store seam. It is a pure orchestration of list → select → remove, so both the
// selection and the removal order are unit-testable without Drive.
func ReconcileDestination(
	ctx context.Context,
	store DestinationArtifactStore,
	folderID string,
	keepFileIDs []string,
) (StaleArtifactReport, error) {
	report := StaleArtifactReport{FolderID: folderID}
	if store == nil || folderID == "" {
		return report, nil
	}
	files, err := store.ListArtifacts(ctx, folderID)
	if err != nil {
		return report, err
	}
	report.Scanned = len(files)
	for _, stale := range staleArtifacts(files, keepFileIDs) {
		if err := store.RemoveArtifact(ctx, stale.FileID); err != nil {
			return report, err
		}
		report.Removed++
		report.RemovedNames = append(report.RemovedNames, stale.Name)
	}
	return report, nil
}

// destinationFolderID resolves the folder that actually holds the run's
// artifacts: the published chunks' parent timestamp folder when known (that is
// where the files live), otherwise the resolved destination folder from the
// input. Empty ⇒ the caller skips reconciliation (nothing to scope the GC to).
func destinationFolderID(in *RunInput, chunks []ChunkState) string {
	for i := range chunks {
		if chunks[i].TimestampFolderID != "" {
			return chunks[i].TimestampFolderID
		}
	}
	if in == nil {
		return ""
	}
	if id := ResolvedFolderID(in); id != "" {
		return id
	}
	return ""
}

// reconcilePublishedDestination runs the post-publish hygiene pass. Nil
// reconciler (test fixtures, un-wired roots) is a no-op. Errors are logged and
// swallowed by design (see the section header): the artifacts are already
// durable.
func reconcilePublishedDestination(ctx context.Context, runner StepRunner, in *RunInput, chunks []ChunkState) {
	reconciler := runner.DestinationReconciler()
	if reconciler == nil || len(chunks) == 0 {
		return
	}
	folderID := destinationFolderID(in, chunks)
	if folderID == "" {
		if runner.Log() != nil {
			runner.Log().Warn("orchestrator: stock.publish: destination reconcile skipped — no folder id on the published chunks")
		}
		return
	}
	keep := make([]string, 0, len(chunks))
	for i := range chunks {
		if chunks[i].RemoteFileID != "" {
			keep = append(keep, chunks[i].RemoteFileID)
		}
	}
	report, err := reconciler.ReconcileDestination(ctx, folderID, keep)
	if err != nil {
		if runner.Log() != nil {
			runner.Log().Warn("orchestrator: stock.publish: destination reconcile failed (artifacts already published; stale siblings survive this run)",
				zap.String("folder_id", folderID),
				zap.Error(err))
		}
		return
	}
	if runner.Log() != nil && report.Removed > 0 {
		runner.Log().Info("orchestrator: stock.publish: destination reconciled — stale artifacts from earlier plans removed",
			zap.String("folder_id", folderID),
			zap.Int("scanned", report.Scanned),
			zap.Int("removed", report.Removed),
			zap.Strings("removed_names", report.RemovedNames))
	}
}
