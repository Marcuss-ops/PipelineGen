// Package stockpipeline — step_extract_clips.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026;
// PR-SPLIT-STEP-EXTRACT-CLIPS, August 2026).
//
// Slim orchestrator for StockExtractClipsStep. The Run() method
// delegates to helper functions in sister files:
//
//	groupPlans        → step_extract_clips_batch.go
//	prepareBatchState → step_extract_clips_batch.go
//	executeCuts       → step_extract_clips_cut.go
//	publishCuts       → step_extract_clips_publish.go
//	writeTimestampGroups → step_extract_clips_metadata.go
//	validateAndProbeSourceDuration → step_extract_clips_validation.go
//	buildRichStockAsset → step_extract_clips_assets.go
package stockpipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// ErrStockClipsOutOfRange (PR-STOCK-TIMESTAMP-CLIPS Front 5, July 2026)
// surfaces a clip whose EndSec exceeds the probed source duration.
var ErrStockClipsOutOfRange = errors.New("stock.extract_clips: clip EndSec exceeds source duration")

// maxDriveUploadWorkers caps concurrent Drive uploads per source group.
const maxDriveUploadWorkers = 3

// StockExtractClipsStep is the canonical implementation of
// stock.extract_clips (Step 3 of the 6-step pipeline).
type StockExtractClipsStep struct{}

func (StockExtractClipsStep) Name() string { return StepKeyStockExtractClips }

func (s StockExtractClipsStep) Run(ctx context.Context, runner StepRunner) error {
	cutter := runner.Cutter()
	plans := runner.State().Plan

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.extract_clips: starting",
			zap.Int("plan_count", len(plans)),
			zap.Int("staged_sources", len(runner.State().StagedAssets)))
	}

	if cutter == nil {
		if len(plans) > 0 {
			return ErrStockExtractClipsCutterRequired
		}
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.extract_clips: VideoCutter nil + empty plan — test-fixture path")
		}
		runner.State().CutPaths = nil
		return nil
	}

	if len(plans) == 0 {
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.extract_clips: empty plan — nothing to extract")
		}
		runner.State().CutPaths = nil
		return nil
	}

	// Build sourceID → *StagedAsset map.
	stagedBySource := make(map[string]*assets.StagedAsset)
	for _, sa := range runner.State().StagedAssets {
		if sa.SourceID != "" && sa.LocalPath != "" {
			sa := sa
			stagedBySource[sa.SourceID] = sa
		}
	}

	in := runner.RunInput()
	// In sections_only mode each plan has its own staged file. The original
	// source URL remains on the plan for provenance; StageKey selects the
	// section-local file used by the cutter.
	grouped := groupPlans(plans)
	if in != nil && in.DownloadMode == "sections_only" {
		grouped = groupPlansByStageKey(plans)
	}
	noAudio := in != nil && in.NoAudio
	batchID := runner.JobID()
	rootFolderName := stockRootFolderName(in)
	rootFolderOverride := stockRootFolderOverride(in)
	timestampGroupName := stockTimestampGroupName(in)
	if in != nil && len(in.Clips) > 0 {
		timestampGroupName = stockTimestampParentGroupName(in)
	}

	var cutPaths []string
	var publishedChunks []ChunkState
	groupBuckets := make(map[string]*timestampGroupBuffer)
	segmentCounts := make(map[string]int)
	contractCounts := make(map[string]int)
	contractDurations := make(map[string]float64)
	sourceIdx := 0
	batchEnsured := false

	for sourceID, groupPlans := range grouped {
		staged := stagedBySource[sourceID]
		if staged == nil {
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.extract_clips: source not staged — skipping",
					zap.String("source_id", sourceID),
					zap.Int("clip_count", len(groupPlans)))
			}
			sourceIdx++
			continue
		}
		sourcePath := staged.LocalPath
		// In sections_only mode sourceID is the section-local staging key.
		// Durable batch/artifact identity must remain tied to the original
		// source URL, otherwise extraction creates rows under the staging key
		// and publication looks them up under the source URL.
		durableSourceID := durableSourceIDForGroup(sourceID, groupPlans)

		// Fase 2 durable state: batch/group/artifact rows.
		var batchErr error
		batchEnsured, batchErr = prepareBatchState(ctx, runner, durableSourceID, groupPlans, len(grouped), len(plans), batchID, batchEnsured)
		if batchErr != nil {
			return batchErr
		}

		// Pre-cut duration validation.
		cutPlans := groupPlans
		if in != nil && in.DownloadMode == "sections_only" {
			cutPlans = make([]ClipPlan, len(groupPlans))
			copy(cutPlans, groupPlans)
			for i := range cutPlans {
				d := cutPlans[i].EndSec - cutPlans[i].StartSec
				cutPlans[i].StartSec = 0
				cutPlans[i].EndSec = d
			}
		}
		sourceDuration, _, validationErr := validateAndProbeSourceDuration(ctx, runner, sourceID, sourcePath, staged, cutPlans)
		if validationErr != nil {
			return validationErr
		}

		// Mark artifacts as extracting.
		if err := markArtifactsExtracting(ctx, runner, batchID, durableSourceID, groupPlans); err != nil {
			return err
		}

		// Execute cuts.
		result, cutErr := executeCuts(ctx, runner, sourceID, sourcePath, sourceDuration, cutPlans, sourceIdx, noAudio)
		successful := result.SuccessfulItems()
		if cutErr != nil && len(successful) == 0 {
			return fmt.Errorf("orchestrator: stock.extract_clips: executeCuts failed for source %s: %w", sourceID, cutErr)
		}
		if in != nil && in.ClipsPerSource > 0 {
			for i, plan := range groupPlans {
				if i < len(result.Items) && result.Items[i].Status != CutItemStatusFailed {
					contractCounts[plan.SourceID]++
					contractDurations[plan.SourceID] += result.Items[i].DurationSec
				}
			}
		}

		// Publish cuts (hash, asset write, Drive upload).
		sourceCutPaths, sourceChunks, pubErr := publishCuts(ctx, runner, durableSourceID, sourceIdx, groupPlans,
			result, segmentCounts, groupBuckets, rootFolderName, rootFolderOverride, timestampGroupName, in, batchID)
		if pubErr != nil {
			return pubErr
		}
		cutPaths = append(cutPaths, sourceCutPaths...)
		publishedChunks = append(publishedChunks, sourceChunks...)

		if runner.Log() != nil {
			runner.Log().Info("orchestrator: stock.extract_clips: source complete",
				zap.String("source_id", sourceID),
				zap.Int("planned", len(groupPlans)),
				zap.Int("produced", len(sourceCutPaths)))
		}
		sourceIdx++
	}
	if in != nil && in.ClipsPerSource > 0 {
		for source := range uniquePlanSources(plans) {
			count := contractCounts[source]
			duration := contractDurations[source]
			if count != in.ClipsPerSource || duration < float64(in.TargetDurationPerSourceSeconds-3) || duration > float64(in.TargetDurationPerSourceSeconds+3) {
				return fmt.Errorf("orchestrator: stock.extract_clips: source %q violates produced duration contract: clips=%d duration=%.3fs", source, count, duration)
			}
		}
	}

	// Publish group metadata.
	if err := writeTimestampGroups(ctx, runner, in, rootFolderName, rootFolderOverride, groupBuckets, runner.ArtifactPreparation()); err != nil {
		return err
	}

	// Production gate: zero cut files → terminal error.
	if len(cutPaths) == 0 {
		return fmt.Errorf("orchestrator: stock.extract_clips: zero cut files produced across %d sources", len(grouped))
	}

	runner.State().CutPaths = cutPaths
	if len(publishedChunks) > 0 {
		runner.State().Published = publishedChunks
	}

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.extract_clips: SUCCEEDED",
			zap.Int("cut_paths", len(cutPaths)),
			zap.Int("sources_processed", sourceIdx))
	}
	return nil
}

// durableSourceIDForGroup keeps SQLite identity on the original source while
// allowing sections_only to use one local staging key per clip.
func durableSourceIDForGroup(stageKey string, plans []ClipPlan) string {
	if len(plans) > 0 && plans[0].SourceID != "" {
		return plans[0].SourceID
	}
	return stageKey
}

func groupPlansByStageKey(plans []ClipPlan) map[string][]ClipPlan {
	grouped := make(map[string][]ClipPlan)
	for _, plan := range plans {
		key := plan.StageKey
		if key == "" {
			key = plan.SourceID
		}
		grouped[key] = append(grouped[key], plan)
	}
	return grouped
}

// groupPlans groups ClipPlan entries by SourceID.
func groupPlans(plans []ClipPlan) map[string][]ClipPlan {
	grouped := make(map[string][]ClipPlan)
	for _, plan := range plans {
		grouped[plan.SourceID] = append(grouped[plan.SourceID], plan)
	}
	return grouped
}

var _ = time.Time{} // pin import
