package youtube

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
)

// writeClipMetadataFile writes and uploads a per-clip metadata file.
// Delegates to the metadata capability service (PR5 Phase 1).
func (s *Service) writeClipMetadataFile(ctx context.Context, clip *asset.Asset, ym *youtubeports.DownloaderMetadata) {
	if s.metadata == nil {
		return
	}
	s.metadata.WriteClipMetadataFile(ctx, clip, ym)
}
