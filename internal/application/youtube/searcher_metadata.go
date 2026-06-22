package youtube

import (
	"context"
	"fmt"
)

// GetVideoInfo retrieves full metadata for a YouTube video without downloading it.
// Uses SearchRunnerPort when wired; falls back to legacy os/exec when not.
// Delegates to the search capability service (PR5 Phase 2).
func (s *Service) GetVideoInfo(ctx context.Context, videoURL string) (*VideoMetadata, error) {
	if s.search == nil {
		return nil, fmt.Errorf("youtube: search service not wired")
	}
	return s.search.GetVideoInfo(ctx, videoURL)
}

