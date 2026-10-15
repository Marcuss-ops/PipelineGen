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
// It is resume-aware across job attempts: a retry of a run whose previous
// attempt died mid-extract inherits stranded rows, and claiming them must
// converge instead of poisoning the retry (the strict PLANNED-only claim
// turned every retried run with partial progress into a deterministic
// failure on its first clip). Artifact IDs are unique per (batch, source,
// clip) — orderedPlanGroups builds one group per source and the mark loop is
// sequential — so an EXTRACTING row seen here can only be an orphan of a dead
// attempt, never a live worker's claim; reclaiming it is race-safe.
func markArtifactsExtracting(ctx context.Context, runner StepRunner, batchID, sourceID string, groupPlans []ClipPlan) error {
	batchRepo := runner.BatchRepository()
	if batchRepo == nil {
		return nil
	}
	for clipIdx := range groupPlans {
		artifactID := StockArtifactID(batchID, sourceID, clipIdx)
		if err := claimArtifactForExtract(ctx, batchRepo, artifactID); err != nil {
			return fmt.Errorf("%w: mark artifact %s extracting: %w", ErrStockExtractClipsDurableStateFailed, artifactID, err)
		}
	}
	return nil
}

// claimArtifactForExtract claims one artifact row for extraction, resuming
// durable progress left by a dead attempt:
//   - missing row: fail-closed (prepareBatchState owns creation; a missing
//     row is a wiring bug, not work to invent);
//   - PLANNED / RETRY_WAIT: normal claim;
//   - EXTRACTING: orphan of a dead attempt — reconcile to RETRY_WAIT first,
//     then claim (attempts keeps counting, so FindIncompleteArtifacts'
//     maxAttempts bound still applies);
//   - EXTRACTED / PUBLISHED / VERIFIED: durable progress — keep as-is. The
//     downstream marks already tolerate repeats (Extracted accepts
//     EXTRACTING|EXTRACTED, Published accepts EXTRACTED|PUBLISHED, Verified
//     accepts PUBLISHED|VERIFIED) and publishCuts skips re-uploading
//     already-published rows, so no duplicate Drive file is produced;
//   - FAILED_PERMANENT / QUARANTINED / anything else: fail-closed, as before.
func claimArtifactForExtract(ctx context.Context, batchRepo StockBatchRepository, artifactID string) error {
	artifact, err := batchRepo.GetArtifact(ctx, artifactID)
	if err != nil {
		return err
	}
	if artifact == nil {
		// Test/back-compat repositories expose no rows (GetArtifact →
		// nil, nil) while accepting MarkArtifactExtracting unconditionally;
		// production MarkArtifactExtracting still fails closed on a missing
		// row, so delegating preserves both contracts.
		return batchRepo.MarkArtifactExtracting(ctx, artifactID)
	}
	switch artifact.Status {
	case ArtifactStateExtracted, ArtifactStatePublished, ArtifactStateVerified:
		return nil
	case ArtifactStateExtracting:
		if err := batchRepo.MarkArtifactFailed(ctx, artifactID, ArtifactStateRetryWait, "reclaim orphaned EXTRACTING claim from a dead attempt"); err != nil {
			return err
		}
	}
	return batchRepo.MarkArtifactExtracting(ctx, artifactID)
}

// publishedRowReusable returns the persisted Drive identity when the artifact
// row already carries a completed publication (PUBLISHED or VERIFIED with a
// Drive file ID) from a dead attempt. Reusing it lets a retry commit the same
// deterministic rows without uploading a duplicate Drive file. ok=false means
// "upload normally"; a non-nil error is fail-closed (the row could not be
// inspected, so the caller must abort rather than risk a duplicate).
func publishedRowReusable(ctx context.Context, batchRepo StockBatchRepository, artifactID string) (fileID, folderID, webLink string, ok bool, err error) {
	if batchRepo == nil {
		return "", "", "", false, nil
	}
	artifact, err := batchRepo.GetArtifact(ctx, artifactID)
	if err != nil {
		return "", "", "", false, err
	}
	if artifact == nil {
		return "", "", "", false, nil
	}
	if artifact.Status != ArtifactStatePublished && artifact.Status != ArtifactStateVerified {
		return "", "", "", false, nil
	}
	if artifact.DriveFileID == "" {
		return "", "", "", false, nil
	}
	return artifact.DriveFileID, artifact.DriveFolderID, artifact.DriveLink, true, nil
}

// markGroupsRetryable reconciles artifacts that were marked EXTRACTING before
// the bounded cut fan-out but did not reach publishCuts. RETRY_WAIT is the
// durable, idempotent state for cancellation or transient cutter failures;
// the next attempt can claim those deterministic artifact IDs again.
func markGroupsRetryable(ctx context.Context, runner StepRunner, batchID string, groups []stockSourceGroup, reason string) error {
	batchRepo := runner.BatchRepository()
	if batchRepo == nil {
		return nil
	}
	for _, group := range groups {
		sourceID := durableSourceIDForGroup(group.sourceID, group.plans)
		for clipIdx := range group.plans {
			artifactID := StockArtifactID(batchID, sourceID, clipIdx)
			artifact, err := batchRepo.GetArtifact(ctx, artifactID)
			if err != nil {
				return fmt.Errorf("%w: inspect artifact %s for retry: %w", ErrStockExtractClipsDurableStateFailed, artifactID, err)
			}
			// Test/back-compat repositories may not expose a row; retain
			// the existing fail-closed transition in that case. Production
			// repositories return the durable row, allowing us to avoid
			// regressing artifacts that were already published before a
			// later publication failed.
			if artifact == nil {
				continue
			}
			if artifact.Status != ArtifactStateExtracting {
				continue
			}
			if err := batchRepo.MarkArtifactFailed(ctx, artifactID, ArtifactStateRetryWait, reason); err != nil {
				return fmt.Errorf("%w: mark artifact %s retryable: %w", ErrStockExtractClipsDurableStateFailed, artifactID, err)
			}
		}
	}
	return nil
}
