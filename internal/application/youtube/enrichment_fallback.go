package youtube

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// buildFallbackSearchText builds a minimal search_text from existing clip metadata.
// Delegates to the metadata capability service (PR5 Phase 1).
func (s *Service) buildFallbackSearchText(clip *asset.Asset) {
	if s.metadata == nil {
		return
	}
	s.metadata.BuildFallbackSearchText(clip)
}
