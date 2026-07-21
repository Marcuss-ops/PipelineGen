// Package artlist adapts internal/sources/artlist.Service to the
// canonical providers.SearchProvider contract in
// internal/application/assets/providers.
//
// Wave 12 scope: this adapter is transitional. The contract will not
// change when artlist migrates to a real domain/application layer,
// but the import path will. Adapter owners update the import path;
// downstream consumers keep using providers.SearchProvider
// unchanged.
//
// Layout note (post-Agent-3 cleanup): artlist lives at
// providers/artlist/adapter.go, parallel to youtube/adapter.go. The
// historical nesting under providers/adapters/<src>/ is removed.
package artlist

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// Compile-time assertion: *Adapter satisfies providers.SearchProvider.
// Catches interface drift at build time. The Adapter intentionally
// does NOT implement FetchProvider (artlist has no public fetch
// binary path; the download pipeline lives in stockpipeline +
// drive upload, out of scope for the Provider contract).
var _ providers.SearchProvider = (*Adapter)(nil)

// ErrSourceNotWired is returned when an Adapter is constructed
// without a non-nil underlying artlist.Service.
var ErrSourceNotWired = errors.New("artlist adapter: source not wired")

// searcher is the minimal internal interface the adapter depends on
// for Search. Defining it private to this package lets the unit tests
// inject a stub without constructing a full *Service.
//
// *Service satisfies searcher via its public Search method.
type searcher interface {
	Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error)
}

// Adapter wraps an artlist searcher (production: *artlistsrc.Service)
// and presents it as a SearchProvider. It does NOT introduce new
// search semantics: it translates the canonical
// providers.SearchRequest into the service's native request at the
// boundary, and translates the SearchResponse back into canonical
// providers.Candidate values.
type Adapter struct {
	src searcher
}

// NewAdapter returns an Adapter wrapping the given service. The
// service must be fully wired (composition root responsibility).
func NewAdapter(src *Service) *Adapter { return &Adapter{src: src} }

// Name implements providers.Provider.
func (a *Adapter) Name() string { return "artlist" }

// Capabilities implements providers.Provider.
// CapabilityFetch is intentionally omitted: artlist's download
// pipeline (videomuscles + drive upload) is not a Provider
// concern. The Search/Fetch split means this adapter ONLY
// satisfies SearchProvider and the registry must not return it
// for ByCapability(CapabilityFetch).
func (a *Adapter) Capabilities() []providers.Capability {
	return []providers.Capability{
		providers.CapabilitySearch,
		providers.CapabilityVideo,
		providers.CapabilityMusic,
	}
}

// Search implements providers.SearchProvider.
//
// Mapping rules:
//
//   - req.Query               -> artlist SearchRequest.Term
//   - req.Limit               -> artlist SearchRequest.Limit
//   - req.TopicOnly           -> not supported (artlist is term-based)
//   - req.Filters.PublishedAfter/Sort/MinDuration/MaxDuration
//     -> not supported on artlist (single global order via the scraper)
//   - req.Filters.MediaTypes  -> not honoured; artlist always returns
//     video/music regardless.
//
// NextPageToken is always empty in the returned SearchResult:
// artlist has no cursor. Callers treat empty as "no more pages".
//
// Why the response is already close to canonical: artlist's
// SearchResponse uses the Asset struct directly (see
// internal/sources/artlist/dto_search.go), so the loop maps each
// Asset into a typed Candidate without inventing new fields. Asset
// canonicalization is the downstream ingest use case's
// responsibility — this adapter returns raw findings only.
func (a *Adapter) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	if err := a.checkWired(); err != nil {
		return providers.SearchResult{}, err
	}

	native := &SearchRequest{
		Term:     req.Query,
		Limit:    req.Limit,
		PreferDB: false,
	}

	resp, err := a.src.Search(ctx, native)
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
			PublishedAt:  nil, // artlist DB record may carry CreatedAt but not publish time
			Score:        0,
		})
	}
	return providers.SearchResult{Candidates: candidates}, nil
}

// checkWired returns ErrSourceNotWired when the adapter has no
// usable searcher. Guards against both nil interface and typed-nil
// pointer (matches the registry's typed-nil convention, consistent
// with youtube/adapter.go).
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

// CachedSearcher wraps a Searcher with in-memory caching.
// Results are cached with a configurable TTL and refreshed in the background
// when the cache is > 75% stale.
type CachedSearcher struct {
	inner Searcher
	cache *liveSearchCache
	ttl   time.Duration
	log   *zap.Logger
}

// NewCachedSearcher creates a new CachedSearcher.
func NewCachedSearcher(inner Searcher, cache *liveSearchCache, ttlHours int, log *zap.Logger) *CachedSearcher {
	if ttlHours <= 0 {
		ttlHours = 24
	}
	return &CachedSearcher{
		inner: inner,
		cache: cache,
		ttl:   time.Duration(ttlHours) * time.Hour,
		log:   log,
	}
}

func (s *CachedSearcher) Search(ctx context.Context, req SearchRequest) ([]Candidate, error) {
	term := req.Term

	// Check cache first
	if s.cache != nil && s.cache.isFresh(term, s.ttl) {
		cached, _ := s.cache.get(term)
		if s.log != nil {
			s.log.Info("artlist search: cache HIT", zap.String("term", term), zap.Int("clips", len(cached)))
		}

		// Background refresh if cache is > 75% of TTL
		if s.cache.isGettingStale(term, s.ttl) {
			if s.log != nil {
				s.log.Info("artlist search: cache getting stale, scheduling background refresh", zap.String("term", term))
			}
			concurrent.SafeGo("artlist-cache-refresh-"+term, func() {
				bgCtx := context.WithoutCancel(ctx)
				if freshClips, err := s.inner.Search(bgCtx, req); err == nil && len(freshClips) > 0 {
					s.cache.set(term, freshClips)
					if s.log != nil {
						s.log.Info("artlist background refresh: cache updated", zap.String("term", term), zap.Int("clips", len(freshClips)))
					}
				} else if err != nil && s.log != nil {
					s.log.Warn("artlist background refresh: live search failed", zap.String("term", term), zap.Error(err))
				}
			})
		}

		limit := req.Limit
		if limit <= 0 {
			limit = 8
		}
		if len(cached) > limit {
			cached = cached[:limit]
		}
		return cached, nil
	}

	// Cache miss: delegate to inner searcher
	candidates, err := s.inner.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(candidates) > 0 && s.cache != nil {
		s.cache.set(term, candidates)
	}
	return candidates, nil
}

// DBSearcher searches the local database for indexed clips.
// It implements Searcher so it can plug directly into SearcherFallbackChain.
type DBSearcher struct {
	store AssetStore
}

// NewDBSearcher creates a new DBSearcher.
func NewDBSearcher(store AssetStore) *DBSearcher {
	return &DBSearcher{store: store}
}

func (s *DBSearcher) Search(ctx context.Context, req SearchRequest) ([]Candidate, error) {
	if s.store == nil {
		return nil, nil
	}
	term := strings.TrimSpace(req.Term)
	if term == "" {
		return nil, nil
	}
	dbClips, err := s.store.SearchClips(ctx, "artlist", term)
	if err != nil {
		return nil, err
	}
	if len(dbClips) == 0 {
		return nil, nil
	}

	candidates := make([]Candidate, 0, len(dbClips))
	for _, clip := range dbClips {
		candidates = append(candidates, Candidate{
			Provider:     "artlist",
			ExternalID:   clip.ID,
			ID:           clip.ID,
			Title:        clip.Name,
			Description:  clip.GetMetadataString("description"),
			Creator:      clip.GetMetadataString("creator"),
			PageURL:      clip.ClipPageURL,
			PreviewURL:   firstNonEmpty(clip.GetMetadataString("preview_url"), clip.ClipPageURL),
			ThumbnailURL: clip.ThumbnailURL,
			SourceRef:    firstNonEmpty(clip.SourceURL, clip.ClipPageURL),
			SourceName:   "database",
			MediaType:    clip.MediaType,
			Duration:     clip.Duration,
			DurationMs:   clip.Duration.Milliseconds(),
			Keywords:     clip.Tags,
			Categories:   stringSliceFromMetadata(clip.Metadata, "provider_categories"),
			RawMetadata:  cloneMetadata(clip.Metadata),
		})
	}
	return candidates, nil
}

// firstNonEmpty returns the first non-empty string, or empty if none.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// cloneMetadata returns a shallow copy of an asset metadata map.
func cloneMetadata(m asset.Metadata) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
