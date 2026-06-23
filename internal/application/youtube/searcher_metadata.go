package youtube

import (
	"context"
	"fmt"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
)

// GetVideoInfo retrieves full metadata for a YouTube video without downloading it.
// Delegates to the search capability service (PR5 Phase 2).
func (s *Service) GetVideoInfo(ctx context.Context, videoURL string) (*youtubeports.DownloaderMetadata, error) {
	if s.search == nil {
		return nil, fmt.Errorf("youtube: search service not wired")
	}
	return s.search.GetVideoInfo(ctx, videoURL)
}
