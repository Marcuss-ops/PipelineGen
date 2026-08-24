// Package artlist adapts the Artlist service to the canonical provider search contract.
package artlist

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
)

// Compile-time assertion: *Adapter satisfies providers.SearchProvider.
// The adapter intentionally does not implement FetchProvider.
var _ providers.SearchProvider = (*Adapter)(nil)

// ErrSourceNotWired is returned when an Adapter has no underlying Artlist service.
var ErrSourceNotWired = errors.New("artlist adapter: source not wired")

type searcher interface {
	Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error)
}

// Adapter translates the native Artlist search contract to the canonical
// providers.SearchProvider contract. It does not introduce search policy.
type Adapter struct {
	src searcher
}

// NewAdapter returns an Adapter wrapping the given service.
func NewAdapter(src *Service) *Adapter { return &Adapter{src: src} }

// Name implements providers.Provider.
func (a *Adapter) Name() string { return "artlist" }

// Capabilities implements providers.Provider.
func (a *Adapter) Capabilities() []providers.Capability {
	return []providers.Capability{
		providers.CapabilitySearch,
		providers.CapabilityVideo,
		providers.CapabilityMusic,
	}
}

// Search implements providers.SearchProvider.
func (a *Adapter) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	if err := a.checkWired(); err != nil {
		return providers.SearchResult{}, err
	}
	// Production Service implements the cache-first live chain. Do not route
	// the canonical facade through the catalog-only Search method.
	if live, ok := a.src.(liveSearcher); ok {
		candidates, err := live.SearchLive(ctx, req.Query, req.Limit, false)
		if err != nil {
			return providers.SearchResult{}, fmt.Errorf("artlist search: %w", err)
		}
		return providers.SearchResult{Candidates: mapLiveCandidates(a.Name(), candidates)}, nil
	}

	resp, err := a.src.Search(ctx, &SearchRequest{
		Term:     req.Query,
		Limit:    req.Limit,
		PreferDB: false,
	})
	if err != nil {
		return providers.SearchResult{}, fmt.Errorf("artlist search: %w", err)
	}
	if resp == nil {
		return providers.SearchResult{}, nil
	}

	candidates := make([]providers.Candidate, 0, len(resp.Clips))
	for i := range resp.Clips {
		clip := &resp.Clips[i]
		previewURL := firstNonEmpty(clip.GetMetadataString("preview_url"), clip.ClipPageURL)
		candidates = append(candidates, providers.Candidate{
			Provider:     a.Name(),
			ExternalID:   clip.ID,
			ID:           clip.ID,
			Title:        clip.Name,
			Description:  clip.GetMetadataString("description"),
			Creator:      clip.GetMetadataString("creator"),
			PageURL:      clip.ClipPageURL,
			PreviewURL:   previewURL,
			ThumbnailURL: clip.ThumbnailURL,
			SourceRef:    firstNonEmpty(clip.SourceURL, clip.ClipPageURL),
			SourceName:   a.Name(),
			MediaType:    clip.MediaType,
			Duration:     clip.Duration,
			DurationMs:   clip.Duration.Milliseconds(),
			Keywords:     clip.Tags,
			Categories:   stringSliceFromMetadata(clip.Metadata, "provider_categories"),
			RawMetadata:  cloneMetadata(clip.Metadata),
			PublishedAt:  nil,
			Score:        0,
		})
	}
	return providers.SearchResult{Candidates: candidates}, nil
}

func mapLiveCandidates(provider string, in []Candidate) []providers.Candidate {
	out := make([]providers.Candidate, 0, len(in))
	for _, clip := range in {
		out = append(out, providers.Candidate{
			Provider:     provider,
			ExternalID:   clip.ID,
			ID:           clip.ID,
			Title:        clip.Title,
			PageURL:      clip.PageURL,
			ThumbnailURL: clip.ThumbnailURL,
			PreviewURL:   clip.PreviewURL,
			// SourceRef is the stable provider identity. The temporary media
			// URL stays out of the identity and is refreshed by resolve.
			SourceRef:  firstNonEmpty(clip.ID, clip.SourceRef),
			Duration:   clip.Duration,
			DurationMs: clip.Duration.Milliseconds(),
			Width:      clip.Width,
			Height:     clip.Height,
			Keywords:   append([]string(nil), clip.Keywords...),
			Categories: append([]string(nil), clip.Categories...),
			MediaType:  clip.MediaType,
			Score:      clip.Score,
		})
	}
	return out
}

func (a *Adapter) checkWired() error {
	if a.src == nil {
		return ErrSourceNotWired
	}
	rv := reflect.ValueOf(a.src)
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return ErrSourceNotWired
	}
	return nil
}
