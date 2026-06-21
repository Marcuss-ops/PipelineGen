package artlist

import (
	"context"
	"time"

	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

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
