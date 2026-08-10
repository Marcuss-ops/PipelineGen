package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"golang.org/x/sync/singleflight"
)

// imageSearchProvider adapts the image retrieval registry to the common
// provider search contract. It is search-only: it returns metadata and never
// calls the image download/ingest path.
type imageSearchProvider struct {
	resolver routing.ImageSearchResolver
	flight   singleflight.Group
}

func newImageSearchProvider(resolver routing.ImageSearchResolver) *imageSearchProvider {
	return &imageSearchProvider{resolver: resolver}
}

func (p *imageSearchProvider) Name() string { return "image" }
func (p *imageSearchProvider) Capabilities() []providers.Capability {
	return []providers.Capability{providers.CapabilitySearch, providers.CapabilityImage}
}

func (p *imageSearchProvider) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	if p == nil || p.resolver == nil {
		return providers.SearchResult{}, errors.New("image search provider not wired")
	}
	key := imageSearchKey(req)
	value, err, _ := p.flight.Do(key, func() (any, error) {
		return p.searchUncached(ctx, req)
	})
	if err != nil {
		return providers.SearchResult{}, err
	}
	return cloneProviderSearchResult(value.(providers.SearchResult)), nil
}

func (p *imageSearchProvider) searchUncached(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	searcher, err := p.resolver.Resolve(routing.TerritoryRetrieved)
	if err != nil {
		return providers.SearchResult{}, err
	}
	hits, err := searcher.Search(ctx, routing.ImageFilter{
		SubjectID: strings.TrimSpace(req.Query),
		Origins:   []asset.ImageOrigin{asset.ImageOriginRetrieved},
		Tags:      append([]string(nil), req.Filters.Tags...),
		Limit:     req.Limit,
	})
	if err != nil {
		return providers.SearchResult{}, err
	}
	out := make([]providers.Candidate, 0, len(hits))
	for _, hit := range hits {
		id := firstNonEmptyProvider(hit.AssetID, hit.SourcePageURL, hit.PreviewURL)
		out = append(out, providers.Candidate{
			Provider: hit.Provider, ExternalID: id, ID: id,
			Title: hit.Name, PageURL: hit.SourcePageURL,
			ThumbnailURL: hit.PreviewURL, PreviewURL: hit.PreviewURL,
			SourceRef: id, Width: hit.Width, Height: hit.Height,
			MediaType: asset.MediaType("image"), Score: hit.Score,
		})
	}
	return providers.SearchResult{Candidates: out}, nil
}

func imageSearchKey(req providers.SearchRequest) string {
	tags := append([]string(nil), req.Filters.Tags...)
	sort.Strings(tags)
	return "image:" + normalizeImageQuery(req.Query) + ":" + strings.Join(tags, ",") + ":" + fmt.Sprint(req.Limit)
}

func normalizeImageQuery(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}

func cloneProviderSearchResult(in providers.SearchResult) providers.SearchResult {
	in.Candidates = append([]providers.Candidate(nil), in.Candidates...)
	return in
}

var _ providers.SearchProvider = (*imageSearchProvider)(nil)
