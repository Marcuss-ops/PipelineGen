// Package usecase — process_segment_step8_upload.go (GOP-06, November 2026).
//
// Step 8 owner: the concurrent subtitle-sidecar + video Drive uploads.
//
// The two uploads have NO data dependency, so they run concurrently under
// errgroup. Drive uploads stay bounded by the fan-out concurrency (one slot
// per segment). This helper NEVER mutates `out` and NEVER calls u.fail: it
// returns the two results, and the caller invokes u.fail exactly once on the
// caller goroutine after Wait.
package usecase

import (
	"context"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// uploadSegmentArtifacts uploads the subtitle sidecar (when a bundle and a
// destination exist) and the video concurrently. The subtitle destination is
// already the final upload target: the extraction orchestrator resolved the
// per-video subfolder once before fan-out, so no per-segment GetOrCreateFolder
// happens here.
func (u *ProcessYouTubeSegmentUseCase) uploadSegmentArtifacts(
	ctx context.Context,
	cmd youtubetypes.ProcessSegmentCommand,
	out *youtubetypes.ProcessSegmentResult,
	bundle *detail.ResolvedTextBundle,
	txtPath, localPath string,
) (subtitleUploadErr error, videoUploadRes *youtubeports.UploadResultDTO, videoUploadErr error) {
	uploadStarted := time.Now()
	uploadGroup, uploadCtx := errgroup.WithContext(ctx)

	if bundle != nil && !bundle.IsEmpty() && cmd.SubtitleFolderID != "" {
		subtitleFolderID := cmd.SubtitleFolderID
		subtitleName := filepath.Base(txtPath)
		uploadGroup.Go(func() error {
			_, _, err := u.media.DriveFolderMgr.UploadFileIfChanged(
				uploadCtx, txtPath, subtitleFolderID, subtitleName,
				deriveNormalizedGroup(cmd), cmd.VideoID,
			)
			subtitleUploadErr = err
			return nil
		})
	}

	if u.media.DriveFolderMgr != nil && cmd.DriveFolderID != "" && localPath != "" {
		uploadGroup.Go(func() error {
			res, _, err := u.media.DriveFolderMgr.UploadFileIfChanged(
				uploadCtx, localPath, cmd.DriveFolderID, out.Item.Filename,
				deriveNormalizedGroup(cmd), cmd.VideoID,
			)
			videoUploadRes, videoUploadErr = res, err
			return nil
		})
	}
	_ = uploadGroup.Wait()

	uploadStageErr := subtitleUploadErr
	if uploadStageErr == nil {
		uploadStageErr = videoUploadErr
	}
	kernobs.RecordStage(ctx, kernobs.StageInfo{
		Stage:          kernobs.StageYouTubeUpload,
		ItemsInput:     1,
		ItemsCompleted: completedCount(uploadStageErr == nil),
	}, uploadStarted, time.Now(), uploadStageErr)

	return subtitleUploadErr, videoUploadRes, videoUploadErr
}

// logDroppedBundle surfaces that an acquired transcript was discarded
// because the upstream Drive upload failed (it will be re-acquired on retry).
func (u *ProcessYouTubeSegmentUseCase) logDroppedBundle(clipID string, bundle *detail.ResolvedTextBundle, err error) {
	if bundle == nil || bundle.IsEmpty() {
		return
	}
	u.core.Log.Warn("text bundle dropped due to upstream Drive upload failure; transcript will be re-acquired on the next retry — no atomic super-tx executed",
		zap.String("clip_id", clipID),
		zap.String("bundle_language", bundle.LanguageCode),
		zap.Int("bundle_cues", len(bundle.Cues)),
		zap.Error(err))
}

// logVideoUploadFailure classifies the video upload error for operators.
func (u *ProcessYouTubeSegmentUseCase) logVideoUploadFailure(clipID string, err error) {
	if IsTransientExtractionError(err) {
		u.core.Log.Warn("drive upload transient failure (will be classified retryable by parent)",
			zap.String("clip_id", clipID), zap.Error(err))
		return
	}
	u.core.Log.Error("drive upload terminal failure (will be classified terminal by parent)",
		zap.String("clip_id", clipID), zap.Error(err))
}
