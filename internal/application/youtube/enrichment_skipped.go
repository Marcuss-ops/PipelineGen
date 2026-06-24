package youtube

import (
	"context"

	"go.uber.org/zap"
)

// enrichSkippedClip enriches a clip that was found in cache (skipped) but lacks YouTube metadata.
// This handles the case where a clip was downloaded in a previous session without metadata
// (e.g., before the yt-dlp metadata fetch was fixed).
//
// Phase 1 of CPR-CC-6 (June 2026): capability service receives the DownloaderMetadata directly.
// facede shim has been deleted (see enrichment.go in the deleted batch). The capability
// service now receives the DownloaderMetadata directly through the root orchestrator's
// `s.metadata.EnrichClip` accessor, which matches the PR5 metadata capability contract.
func (s *Service) enrichSkippedClip(ctx context.Context, clipID, videoURL, videoID string) {
	// Check if clip needs enrichment
	existing, err := s.clips.GetClip(ctx, clipID)
	if err != nil || existing == nil {
		return
	}
	// If already has YouTube metadata, skip
	if existing.GetMetadataString("youtube_title") != "" {
		return
	}

	s.log.Info("enriching skipped YouTube clip with metadata",
		zap.String("clip_id", clipID),
		zap.String("video_id", videoID))

	// Fetch YouTube metadata directly via the metaFetcher port
	if s.metaFetcher == nil {
		return
	}
	ym, err := s.metaFetcher.GetVideoMetadata(ctx, videoURL)
	if err != nil {
		s.log.Warn("failed to fetch YouTube metadata for skipped clip",
			zap.String("clip_id", clipID),
			zap.Error(err))
		return
	}

	// Pass DownloaderMetadata directly to the metadata capability service
	// `the deleted capability brokering shim` facede shim).
	s.metadata.EnrichClip(ctx, clipID, ym, false)

	// Also trigger auto-indexing now that the clip has rich search_text
	s.triggerAutoIndexing(ctx, clipID)
}
