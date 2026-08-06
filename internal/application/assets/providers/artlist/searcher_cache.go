package artlist

import (
	"context"
	"errors"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
	inner    Searcher
	cache    *liveSearchCache
	ttl      time.Duration
	log      *zap.Logger
	enqueuer RefreshEnqueuer
}

// RefreshEnqueuer is the narrow durable-job port used by the cache decorator.
// Enqueue is intentionally synchronous and fast: only the durable intent is
// persisted on the request path; provider I/O runs in the worker.
type RefreshEnqueuer interface {
	Enqueue(context.Context, *job.EnqueueRequest) (*job.Job, error)
}

// NewCachedSearcher creates a cached search decorator.
func NewCachedSearcher(inner Searcher, cache *liveSearchCache, ttlHours int, log *zap.Logger, enqueuers ...RefreshEnqueuer) *CachedSearcher {
	if ttlHours <= 0 {
		ttlHours = 24
	}
	var enqueuer RefreshEnqueuer
	if len(enqueuers) > 0 {
		enqueuer = enqueuers[0]
	}
	return &CachedSearcher{
		inner:    inner,
		cache:    cache,
		ttl:      time.Duration(ttlHours) * time.Hour,
		log:      log,
		enqueuer: enqueuer,
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
			if s.enqueuer != nil {
				if s.log != nil {
					s.log.Info("artlist search: cache getting stale, enqueueing durable refresh", zap.String("term", term))
				}
				refreshTerm := normalizeSearchTermLower(term)
				// Refreshes use the canonical provider limit so one durable job
				// warms the cache for all callers, regardless of the stale-hit
				// request's display limit.
				const refreshLimit = 50
				refreshReq := &job.EnqueueRequest{
					Type:      media.TypeArtlistCacheRefresh,
					Payload:   appjobs.ArtlistCacheRefreshPayload{Term: refreshTerm, Limit: refreshLimit, PreferRemote: true},
					ActiveKey: "artlist-cache-refresh:" + refreshTerm,
				}
				if _, err := s.enqueuer.Enqueue(ctx, refreshReq); err != nil && s.log != nil {
					s.log.Warn("artlist cache refresh enqueue failed", zap.String("term", term), zap.Error(err))
				}
			} else if s.log != nil {
				s.log.Warn("artlist cache refresh skipped: durable job enqueuer is not wired", zap.String("term", term))
			}
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
