package youtube

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/media/videomuscles"
	"github.com/Marcuss-ops/PipelineGen/pkg/media/downloader"

	"go.uber.org/zap"
)

// enrichSkippedClip enriches a clip that was found in cache (skipped) but lacks YouTube metadata.
// This handles the case where a clip was downloaded in a previous session without metadata
// (e.g., before the yt-dlp metadata fetch was fixed).
func (s *Service) enrichSkippedClip(ctx context.Context, clipID, videoURL, videoID string) {
	// Check if clip needs enrichment
	existing, err := s.clipsRepo.GetClip(ctx, clipID)
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

	// Fetch YouTube metadata directly
	ytDlp := downloader.NewYTDLP(s.cfg)
	meta, err := ytDlp.GetVideoMetadata(ctx, videoURL)
	if err != nil {
		s.log.Warn("failed to fetch YouTube metadata for skipped clip",
			zap.String("clip_id", clipID),
			zap.Error(err))
		return
	}

	// Build result with just metadata (no local path needed for enrichment)
	result := &videomuscles.YouTubeCutResult{
		Metadata: meta,
	}

	s.enrichYouTubeClipWithMetadata(ctx, clipID, result, false)

	// Also trigger auto-indexing now that the clip has rich search_text
	s.triggerAutoIndexing(ctx, clipID)
}
