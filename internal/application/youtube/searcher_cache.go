package youtube

import (
	"context"
	"fmt"
)

// PrewarmHotVideoMetadataCache pre-warms the L1 in-memory cache with the top 20
// hottest entries from the L2 SQLite cache. Delegates to the search capability
// service (PR5 Phase 2).
func (s *Service) PrewarmHotVideoMetadataCache(ctx context.Context) error {
	if s.search == nil {
		return fmt.Errorf("search service not available")
	}
	return s.search.PrewarmHotVideoMetadataCache(ctx)
}
