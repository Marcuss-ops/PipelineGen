package youtube

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// SearchLive performs a live YouTube search using the SearchRunnerPort.
// sort can be "views" for most viewed videos.
// Delegates to the search capability service (PR5 Phase 2).
func (s *Service) SearchLive(ctx context.Context, query string, limit int, sort string) ([]asset.Asset, error) {
	if s.search == nil {
		return nil, fmt.Errorf("youtube: search service not wired")
	}
	return s.search.SearchLive(ctx, query, limit, sort)
}
