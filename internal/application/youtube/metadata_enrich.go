package youtube

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// writeClipMetadataFile writes and uploads a per-clip metadata file.
// Delegates to the metadata capability service (PR5 Phase 1).
func (s *Service) writeClipMetadataFile(ctx context.Context, clip *asset.Asset, ym *YouTubeMetadataPort) {
	if s.metadata == nil {
		return
	}
	s.metadata.WriteClipMetadataFile(ctx, clip, ym)
}
