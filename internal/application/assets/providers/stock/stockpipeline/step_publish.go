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

	"go.uber.org/zap"
)

// StockPublishStep is the canonical implementation of
// stock.publish. §12-7 replaces the §12-5 Begin/Complete stub
// with the real AssetPreparation ladder (delegated to the
// publishChunkPhase + publishMetadataPhase sister files via
// same-package visibility — no new exported symbols).
type StockPublishStep struct{}

func (StockPublishStep) Name() string { return StepKeyStockPublish }

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
		runner.Log().Info("orchestrator: stock.publish: starting",
			zap.Int("composed_paths", len(runner.State().ComposedPaths)))
		runner.Log().Info("orchestrator: stock.publish: AssetPreparation wired — preparing chunks + metadata")
	}

	in := runner.RunInput()
	fp := runner.RunFingerprint()
	explicitTimestamps := in != nil && len(in.Clips) > 0
	rootFolderName := stockRootFolderName(in)
	rootFolderOverride := stockRootFolderOverride(in)
	timestampGroupName := stockTimestampGroupName(in)
	explicitTimestampGroupName := stockTimestampParentGroupName(in)
	if explicitTimestamps {
		timestampGroupName = explicitTimestampGroupName
	}

	// Phase 1: per-chunk AssetPreparation ladder (see
	// step_publish_chunks_phase.go). Returns built ChunkState
	// slice (or existingPublished copy when publishedReady).
	chunks, err := publishChunkPhase(ctx, runner, in, fp, explicitTimestamps,
		rootFolderName, rootFolderOverride, timestampGroupName,
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
		rootFolderName, rootFolderOverride, timestampGroupName, chunks)
	if err != nil {
		return err
	}
	runner.State().MetadataPublished = metadataState

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.publish: SUCCEEDED",
			zap.Int("chunk_count", len(chunks)),
			zap.String("metadata_artifact_id", MetadataArtifactID(fp)),
			zap.String("metadata_remote_file_id", metadataState.RemoteFileID))
	}
	return nil
}
