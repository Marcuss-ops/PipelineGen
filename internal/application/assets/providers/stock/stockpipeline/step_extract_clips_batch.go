package stockpipeline

import (
	"context"

	"go.uber.org/zap"
)

// prepareBatchState ensures batch, group, and artifact rows exist in the
// durable batch repository. Returns batchEnsured=true after first call.
func prepareBatchState(ctx context.Context, runner StepRunner, sourceID string, groupPlans []ClipPlan, numGroups, numPlans int, batchID string, batchEnsured bool) bool {
	batchRepo := runner.BatchRepository()
	if batchRepo == nil {
		return batchEnsured
	}

	if !batchEnsured {
		if batchErr := batchRepo.CreateBatch(ctx, &StockBatch{
			ID:             batchID,
			Fingerprint:    runner.RunFingerprint(),
			SourceURL:      sourceID,
			Status:         BatchStateRunning,
			ExpectedGroups: numGroups,
			ExpectedClips:  numPlans,
		}); batchErr != nil && runner.Log() != nil {
			runner.Log().Warn("orchestrator: stock.extract_clips: failed to create batch row",
				zap.String("batch_id", batchID), zap.Error(batchErr))
		}
		batchEnsured = true
	}

	groupID := StockArtifactGroupID(batchID, sourceID)
	if groupErr := batchRepo.CreateGroup(ctx, &StockBatchGroup{
		ID:            groupID,
		BatchID:       batchID,
		GroupKey:      sourceID,
		Status:        GroupStateRunning,
		ExpectedClips: len(groupPlans),
	}); groupErr != nil && runner.Log() != nil {
		runner.Log().Warn("orchestrator: stock.extract_clips: failed to create group row",
			zap.String("group_id", groupID), zap.Error(groupErr))
	}

	for clipIdx, plan := range groupPlans {
		artifactID := StockArtifactID(batchID, sourceID, clipIdx)
		if artErr := batchRepo.CreateArtifact(ctx, &StockArtifact{
			ID:          artifactID,
			BatchID:     batchID,
			GroupID:     groupID,
			Ordinal:     clipIdx,
			ArtifactKey: plan.OutputLogicalID,
			SourceURL:   plan.SourceID,
			StartSec:    plan.StartSec,
			EndSec:      plan.EndSec,
			Status:      ArtifactStatePlanned,
		}); artErr != nil && runner.Log() != nil {
			runner.Log().Warn("orchestrator: stock.extract_clips: failed to create artifact row",
				zap.String("artifact_id", artifactID), zap.Error(artErr))
		}
	}

	return batchEnsured
}

// markArtifactsExtracting marks all artifacts for a source group as EXTRACTING.
func markArtifactsExtracting(ctx context.Context, runner StepRunner, batchID, sourceID string, groupPlans []ClipPlan) {
	batchRepo := runner.BatchRepository()
	if batchRepo == nil {
		return
	}
	for clipIdx := range groupPlans {
		artifactID := StockArtifactID(batchID, sourceID, clipIdx)
		_ = batchRepo.MarkArtifactExtracting(ctx, artifactID)
	}
}
