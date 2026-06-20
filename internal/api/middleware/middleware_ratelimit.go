package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/gin-gonic/gin"
)

// tokenBucketRateLimiter implements a simple token-bucket per IP.
// It is O(1) for Allow, O(1) for cleanup, and avoids the quadratic
// sort and unbounded slice growth of the old implementation.
type tokenBucketRateLimiter struct {
	mu       sync.RWMutex
	buckets  map[string]*bucket
	limit    int
	window   time.Duration
	maxKeys  int
	stopCh   chan struct{}
	stopOnce sync.Once
}

type bucket struct {
	tokens  int
	lastRef time.Time
}

func newTokenBucketRateLimiter(limit int, window time.Duration) *tokenBucketRateLimiter {
	return &tokenBucketRateLimiter{
		buckets: make(map[string]*bucket),
		limit:   limit,
		window:  window,
		maxKeys: 10000,
		stopCh:  make(chan struct{}),
	}
}

func (rl *tokenBucketRateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{tokens: rl.limit - 1, lastRef: now}
		rl.buckets[key] = b
		return true
	}

	// Refill tokens based on elapsed time since last request
	elapsed := now.Sub(b.lastRef)
	refill := int(elapsed / rl.window * time.Duration(rl.limit))
	if refill > 0 {
		b.tokens += refill
		if b.tokens > rl.limit {
			b.tokens = rl.limit
		}
	}
	b.lastRef = now

	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

// Cleanup removes stale buckets and limits map size.
func (rl *tokenBucketRateLimiter) Cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.cleanupOnce()
		}
	}
}

func (rl *tokenBucketRateLimiter) cleanupOnce() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	for k, b := range rl.buckets {
		if b.lastRef.Before(cutoff) {
			delete(rl.buckets, k)
		}
	}

	// If still over maxKeys, evict oldest entries.
	if len(rl.buckets) > rl.maxKeys {
		for k := range rl.buckets {
			delete(rl.buckets, k)
			if len(rl.buckets) <= rl.maxKeys {
				break
			}
		}
	}
}

func (rl *tokenBucketRateLimiter) Stop() {
	rl.stopOnce.Do(func() {
		close(rl.stopCh)
	})
}

// RateLimitMiddleware holds the middleware and its associated rate limiter
type RateLimitMiddleware struct {
	Handler gin.HandlerFunc
	limiter *tokenBucketRateLimiter
}

// Stop signals the rate limiter's cleanup goroutine to terminate
func (r *RateLimitMiddleware) Stop() {
	if r.limiter != nil {
		r.limiter.Stop()
	}
}

// RateLimit creates a rate limiting middleware. The returned RateLimitMiddleware
// must have its Stop() method called during server shutdown to prevent goroutine leaks.
func RateLimit(cfg *config.Config) *RateLimitMiddleware {
	if !cfg.Security.RateLimitEnabled {
		return &RateLimitMiddleware{
			Handler: func(c *gin.Context) {
				c.Next()
			},
		}
	}

	limiter := newTokenBucketRateLimiter(cfg.Security.RateLimitRequests, time.Minute)

	// Start periodic cleanup (manages its own ticker and stop channel)
	go limiter.Cleanup()

	return &RateLimitMiddleware{
		Handler: func(c *gin.Context) {
			key := c.ClientIP()
			if !limiter.Allow(key) {
				c.JSON(http.StatusTooManyRequests, gin.H{
					"ok":    false,
					"error": "Rate limit exceeded",
				})
				c.Abort()
				return
			}
			c.Next()
		},
		limiter: limiter,
	}
}
