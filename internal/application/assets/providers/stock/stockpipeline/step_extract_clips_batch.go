package stockpipeline

import (
	"context"
	"fmt"
)

// prepareBatchState ensures batch, group, and artifact rows exist in the
// durable batch repository. Any durable write error aborts the step instead
// of allowing the orchestrator to mark stock.extract_clips completed while
// its lifecycle rows are incomplete.
func prepareBatchState(ctx context.Context, runner StepRunner, sourceID string, groupPlans []ClipPlan, numGroups, numPlans int, batchID string, batchEnsured bool) (bool, error) {
	batchRepo := runner.BatchRepository()
	if batchRepo == nil {
		return batchEnsured, nil
	}

	if !batchEnsured {
		if batchErr := batchRepo.CreateBatch(ctx, &StockBatch{
			ID:             batchID,
			Fingerprint:    runner.RunFingerprint(),
			SourceURL:      sourceID,
			Status:         BatchStateRunning,
			ExpectedGroups: numGroups,
			ExpectedClips:  numPlans,
		}); batchErr != nil {
			return false, fmt.Errorf("%w: create batch %s: %w", ErrStockExtractClipsDurableStateFailed, batchID, batchErr)
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
	}); groupErr != nil {
		return false, fmt.Errorf("%w: create group %s: %w", ErrStockExtractClipsDurableStateFailed, groupID, groupErr)
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
		}); artErr != nil {
			return false, fmt.Errorf("%w: create artifact %s: %w", ErrStockExtractClipsDurableStateFailed, artifactID, artErr)
		}
	}

	return batchEnsured, nil
}

// markArtifactsExtracting marks all artifacts for a source group as EXTRACTING.
func markArtifactsExtracting(ctx context.Context, runner StepRunner, batchID, sourceID string, groupPlans []ClipPlan) error {
	batchRepo := runner.BatchRepository()
	if batchRepo == nil {
		return nil
	}
	for clipIdx := range groupPlans {
		artifactID := StockArtifactID(batchID, sourceID, clipIdx)
		if err := batchRepo.MarkArtifactExtracting(ctx, artifactID); err != nil {
			return fmt.Errorf("%w: mark artifact %s extracting: %w", ErrStockExtractClipsDurableStateFailed, artifactID, err)
		}
	}
	return nil
}
