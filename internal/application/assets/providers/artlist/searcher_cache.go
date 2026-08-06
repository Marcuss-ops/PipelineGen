package artlist

import (
	"context"
	"errors"
	"time"

	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// ErrCachedSearcherNotWired is returned when the decorator has no wrapped
// searcher. A missing cache remains valid pass-through mode for tests and
// deployments that intentionally run without persistence.
var ErrCachedSearcherNotWired = errors.New("artlist cached searcher: inner searcher not wired")

// CachedSearcher decorates a Searcher with the Artlist L1/L2 cache policy.
// It owns cache hit, stale-refresh, and cache-miss behavior; the wrapped
// searcher remains responsible only for obtaining candidates.
type CachedSearcher struct {
	inner Searcher
	cache *liveSearchCache
	ttl   time.Duration
	log   *zap.Logger
}

// NewCachedSearcher creates a cached search decorator.
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
	if s == nil || s.inner == nil {
		return nil, ErrCachedSearcherNotWired
	}
	term := req.Term

	if !req.ForceRefresh && s.cache != nil && s.cache.isFresh(term, s.ttl) {
		cached, _ := s.cache.get(term)
		if s.log != nil {
			s.log.Info("artlist search: cache HIT", zap.String("term", term), zap.Int("clips", len(cached)))
		}

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

	candidates, err := s.inner.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(candidates) > 0 && s.cache != nil {
		s.cache.set(term, candidates)
	}
	return candidates, nil
}
