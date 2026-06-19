package youtube

import (
	"context"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// checkExistingClip implements the cache strategy policy for segment
// extraction. Returns true when the existing DB record (and on-disk file)
// matches expectations and the caller should skip the cut/download steps.
// Returns false when the strategy is "replace" or when the existing
// record is stale (file missing or path mismatch).
func (s *Service) checkExistingClip(ctx context.Context, req *ExtractRequest, clipID string, item *ExtractItem, outDir string) bool {
	if req.Strategy == "replace" || s.clipsRepo == nil {
		return false
	}

	existingClip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil || existingClip == nil {
		return false
	}

	expectedPath := filepath.Join(outDir, item.Filename)
	if existingClip.LocalPath() != expectedPath {
		s.log.Info("cached clip local path mismatch, forcing re-processing for new folder",
			zap.String("clip_id", clipID),
			zap.String("existing_path", existingClip.LocalPath()),
			zap.String("expected_path", expectedPath))
		return false
	}

	if req.Strategy == "skip" {
		item.LocalPath = existingClip.LocalPath()
		item.DriveLink = existingClip.DriveLink()
		item.DriveFileID = existingClip.DriveFileID()
		item.DownloadLink = existingClip.DownloadLink()
		item.Status = "skipped"
		return true
	}

	// Default strategy: verify file exists
	if ok, clipErr := fileutil.UsableCachedClip(existingClip.LocalPath()); clipErr == nil && ok {
		item.LocalPath = existingClip.LocalPath()
		item.DriveLink = existingClip.DriveLink()
		item.DriveFileID = existingClip.DriveFileID()
		item.DownloadLink = existingClip.DownloadLink()
		item.Status = "skipped"
		return true
	}

	// Stale record — clean up before reprocessing
	s.log.Warn("stale youtube clip record detected, removing it before reprocessing",
		zap.String("clip_id", clipID),
		zap.String("local_path", existingClip.LocalPath()))
	if existingClip.LocalPath() != "" {
		_ = os.Remove(existingClip.LocalPath())
	}
	_ = s.clipsRepo.DeleteClip(ctx, clipID)
	return false
}
