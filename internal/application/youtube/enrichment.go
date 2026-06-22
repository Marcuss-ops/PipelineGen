package youtube

import (
	"context"
)

// enrichYouTubeClipWithMetadata updates a clip's metadata with YouTube video
// information. Delegates to the metadata capability service (PR5 Phase 1).
func (s *Service) enrichYouTubeClipWithMetadata(ctx context.Context, clipID string, meta *VideoCutResult, force bool) {
	if s.metadata == nil {
		return
	}
	var ym *DownloaderMetadata
	if meta != nil {
		ym = meta.Metadata
	}
	s.metadata.EnrichClip(ctx, clipID, ym, force)
}
