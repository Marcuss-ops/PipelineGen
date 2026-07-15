package deletion

import (
	"context"
	"fmt"
	"regexp"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

var driveLinkFileIDPattern = regexp.MustCompile(`/d/([A-Za-z0-9_-]+)`)

// CompleteAsset performs the post-state-machine closeout. The completion
// capability owns both the external Drive confirmation and atomic cleanup.
func (s *DeletionService) CompleteAsset(ctx context.Context, assetID string) error {
	if assetID == "" {
		return fmt.Errorf("complete asset: asset_id is required (terminal — retry cannot conjure an id)")
	}
	logger := s.log
	if logger == nil {
		logger = zap.NewNop()
	}
	logger.Info("complete asset: starting", zap.String("asset_id", assetID))

	if s.repositories.Clips == nil {
		return fmt.Errorf("complete asset: clipsRepo not wired (production wiring must supply *assets.ClipsRepository)")
	}
	clip, err := s.repositories.Clips.Get(ctx, assetID)
	if err != nil {
		logger.Warn("complete asset: pre-flight Get failed (retryable — no TX will run)", zap.String("asset_id", assetID), zap.Error(err))
		return fmt.Errorf("complete asset pre-flight Get(%s): %w", assetID, err)
	}
	if clip == nil {
		logger.Info("complete asset: row absent — treat as success (idempotent re-run)", zap.String("asset_id", assetID))
		return nil
	}

	currentState := clip.LifecycleState
	if !isPastDriveDeleted(currentState) {
		return fmt.Errorf(
			"complete asset: drive_file_alive_block guard fired (asset row at %q instead of the expected post-Drive-confirmation state {DRIVE_DELETED, INDEX_DELETE_PENDING, INDEX_DELETED, DELETED}); the COMPLETED step requires the canonical state machine to have reached terminal DELETED first; the user's Drive file is masih alive / still alive; do NOT call CompleteAsset until DriveDeleteHandler has stamped DRIVE_DELETED and the IndexDeleteHandler chain has reached DELETED",
			currentState,
		)
	}

	fileID := extractDriveFileID(clip)
	if fileID != "" && s.completion.DriveGone != nil {
		gone, driveErr := s.completion.DriveGone.CheckDriveGone(ctx, fileID)
		if driveErr != nil {
			return fmt.Errorf(
				"complete asset: Drive-gone check for %q failed (no SQLite delete, no outbox purge will run; the state machine stays at %q until the Drive API issue is resolved and CompleteAsset is retried): %w",
				fileID, currentState, driveErr,
			)
		}
		if !gone {
			return fmt.Errorf(
				"complete asset: drive_file_alive_guard_recheck fired for %q (the user's Drive file is still present despite the lifecycle_state=DELETED stamp; the canonical cleanup cannot proceed until DriveDeleteHandler confirms the file's removal in Drive; retry only after operator verifies the Drive side is complete)",
				fileID,
			)
		}
	}

	if s.completion.Tx == nil {
		return fmt.Errorf("complete asset: completionTxRunner not wired (production wiring must supply a CompletionTxRunner satisfying the DELETE FROM media_assets + DELETE FROM outbox_events atomic-tx contract; pre-commit-4/3 wiring forward-pointer — see CHANGELOG honest-limitation)")
	}
	if err := s.completion.Tx.RunCompletionTx(ctx, assetID); err != nil {
		return fmt.Errorf(
			"complete asset: atomic cleanup TX for %q failed (TX rolled back, no media row deleted, no outbox events purged, state machine unchanged): %w",
			assetID, err,
		)
	}
	logger.Info("asset_completed (Blocco 3.1 commit 3/3 — COMPLETED step)", zap.String("asset_id", assetID), zap.String("file_id", fileID))
	return nil
}

func extractDriveFileID(clip *asset.Asset) string {
	if clip == nil {
		return ""
	}
	if id := clip.DriveFileID(); id != "" {
		return id
	}
	for _, link := range []string{clip.DriveLink(), clip.DownloadLink()} {
		if link == "" {
			continue
		}
		match := driveLinkFileIDPattern.FindStringSubmatch(link)
		if len(match) >= 2 {
			return match[1]
		}
	}
	return ""
}

func isPastDriveDeleted(state asset.LifecycleState) bool {
	switch state {
	case asset.StateDriveDeleted,
		asset.StateLifecycleIndexDeletePending,
		asset.StateIndexDeleted,
		asset.StateDeleted,
		asset.LifecycleState("deleted"):
		return true
	default:
		return false
	}
}
