package assets

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
)

// LiveAdapter exposes the canonical Artlist live-search chain through the
// provider registry. The regular Adapter intentionally keeps its historical
// database-search semantics; VidRush must use this adapter so a discovery
// query reaches the scraper instead of silently reading only SQLite.
type LiveAdapter struct {
	src liveSearcher
}

type liveSearcher interface {
	SearchLive(context.Context, string, int, bool) ([]Candidate, error)
}

type forceRefreshLiveSearcher interface {
	SearchLiveForceRefresh(context.Context, string, int, bool) ([]Candidate, error)
}

// NewLiveAdapter creates a provider-registry adapter backed by Service's
// live search path. Composition remains the only place that selects the
// live-vs-catalog behavior.
func NewLiveAdapter(src *Service) *LiveAdapter { return &LiveAdapter{src: src} }

var _ providers.SearchProvider = (*LiveAdapter)(nil)

func (a *LiveAdapter) Name() string { return "artlist" }

func (a *LiveAdapter) Capabilities() []providers.Capability {
	return []providers.Capability{
		providers.CapabilitySearch,
		providers.CapabilityVideo,
		providers.CapabilityMusic,
	}
}

func (a *LiveAdapter) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	if a == nil || a.src == nil {
		return providers.SearchResult{}, ErrSourceNotWired
	}
	if req.Limit <= 0 {
		req.Limit = 8
	}
	var candidates []Candidate
	var err error
	if fresh, ok := a.src.(forceRefreshLiveSearcher); ok {
		candidates, err = fresh.SearchLiveForceRefresh(ctx, req.Query, req.Limit, true)
	} else {
		// Keep test and legacy implementations compatible, but production
		// Service wiring always takes the force-refresh path above.
		candidates, err = a.src.SearchLive(ctx, req.Query, req.Limit, true)
	}
	if err != nil {
		return providers.SearchResult{}, err
	}
	return providers.SearchResult{Candidates: candidates}, nil
}
