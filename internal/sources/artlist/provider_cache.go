package artlist

import (
	"context"
	"time"

	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

// CachedScraperProvider wraps a ScraperProvider with in-memory caching.
// Results are cached with a configurable TTL and refreshed in the background
// when the cache is > 75% stale.
type CachedScraperProvider struct {
	inner *ScraperProvider
	cache *liveSearchCache
	ttl   time.Duration
	log   *zap.Logger
}

// NewCachedScraperProvider creates a new CachedScraperProvider.
func NewCachedScraperProvider(inner *ScraperProvider, cache *liveSearchCache, ttlHours int, log *zap.Logger) *CachedScraperProvider {
	if ttlHours <= 0 {
		ttlHours = 24
	}
	return &CachedScraperProvider{
		inner: inner,
		cache: cache,
		ttl:   time.Duration(ttlHours) * time.Hour,
		log:   log,
	}
}

func (p *CachedScraperProvider) Name() string { return "cached_scraper" }

func (p *CachedScraperProvider) Search(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	// Check cache first
	if p.cache != nil && p.cache.isFresh(term, p.ttl) {
		cached, _ := p.cache.get(term)
		p.log.Info("artlist search: cache HIT", zap.String("term", term), zap.Int("clips", len(cached)))

		// Background refresh if cache is > 75% of TTL
		if p.cache.isGettingStale(term, p.ttl) {
			p.log.Info("artlist search: cache getting stale, scheduling background refresh", zap.String("term", term))
			concurrent.SafeGo("artlist-cache-refresh-"+term, func() {
				bgCtx := context.WithoutCancel(ctx)
				if freshClips, err := p.inner.Search(bgCtx, term, limit); err == nil && len(freshClips) > 0 {
					p.cache.set(term, freshClips)
					p.log.Info("artlist background refresh: cache updated", zap.String("term", term), zap.Int("clips", len(freshClips)))
				} else if err != nil {
					p.log.Warn("artlist background refresh: live search failed", zap.String("term", term), zap.Error(err))
				}
			})
		}

		if len(cached) > limit {
			cached = cached[:limit]
		}
		return cached, nil
	}

	// Cache miss: delegate to inner scraper
	clips, err := p.inner.Search(ctx, term, limit)
	if err != nil {
		return nil, err
	}
	if len(clips) > 0 && p.cache != nil {
		p.cache.set(term, clips)
	}
	return clips, nil
}
