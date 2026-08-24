package middleware

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	"github.com/gin-gonic/gin"
)

// voiceoverBurstBypassEnvKey is the canonical env-var name for the
// voiceover POST burst bypass (godlike/06 SSOT one canonical owner per
// fact). When set to "1" the rate-limit middleware lets through ALL
// /api/media/voiceover/* requests without consuming a token. Production
// deployments leave this unset; E2E runners (Steps [3]+[6] of the
// voiceover E2E plan, August 2026) explicitly opt in. godlike/07
// NO-FAKE-AVAILABILITY: the env-var is read at ctor time (stable
// across the ctor's lifetime); agents MUST NOT enable this env
// without an explicit operator directive.
const voiceoverBurstBypassEnvKey = "VELOX_VOICEOVER_RATE_LIMIT_BURST"

// voiceoverBurstBypassRoutePrefix is the canonical route prefix gated
// by the env-var. Only /api/media/voiceover/* paths match; all other
// routes remain subject to the per-IP token bucket (godlike/07
// minimum-blast-radius: bypass MUST NOT silently weaken production
// rate-limits on other surfaces).
const voiceoverBurstBypassRoutePrefix = "/api/media/voiceover"

// isVoiceoverBurstBypassRoute reports whether c.FullPath() falls under
// the canonical voiceover route prefix. Returning false on
// c.FullPath() == "" is intentional (godlike/07 NO-FAKE-AVAILABILITY):
// routes mounted-but-not-matched return empty FullPath() and MUST be
// treated as not-voiceover rather than accidentally bypassed.
//
// Match is path-segment aware: the prefix match is bounded by either an
// exact match (`/api/media/voiceover`) OR a `/` boundary after the
// prefix (`/api/media/voiceover/generate`). Naive strings.HasPrefix
// would silently widen the bypass to lookalike routes such as
// `/api/media/voiceover-evil/` or `/api/media/voiceover2/v1` (any
// future text-prefixed variant) — exactly the silent-success pattern
// godlike/07 NO-FAKE-AVAILABILITY forbids.
func isVoiceoverBurstBypassRoute(c *gin.Context) bool {
	path := c.FullPath()
	if path == "" {
		return false
	}
	return path == voiceoverBurstBypassRoutePrefix ||
		strings.HasPrefix(path, voiceoverBurstBypassRoutePrefix+"/")
}

// voiceoverBurstBypassEnabled reads the canonical env-var at ctor
// time via the EnvReader port (no direct os.Getenv). Reading at ctor
// time (not at request time) keeps the verdict stable across the
// ctor's lifetime; operators who change the env mid-flight pay the
// cost of one server restart. This matches the canonical "fix minimo"
// discipline (godlike/07).
func voiceoverBurstBypassEnabled(env EnvReader) bool {
	if env == nil {
		return false
	}
	return env.Getenv(voiceoverBurstBypassEnvKey) == "1"
}

// tokenBucketRateLimiter implements a simple token-bucket per IP.
// O(1) for Allow, O(1) for cleanup, and avoids the quadratic sort
// and unbounded slice growth of the old implementation.
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

// Allow checks whether the bucket for `key` admits one token and,
// atomically under rl.mu, returns the gate decision plus the honest
// time the caller would have to wait for the next refill (godlike/07
// fail-closed contract on retry hints):
//   - allowed=true,  retryAfter=0  on success (one token was consumed)
//   - allowed=false, retryAfter>0  on deny;  retryAfter is the time
//     until the bucket would admit one token. Coerced to integer
//     delta-seconds by the HTTP layer per RFC 7231 §7.1.3.
//
// The honest retryAfter reflects the actual token-bucket arithmetic:
// the bucket refills `rl.limit` tokens every `rl.window`, so the
// next refill is at the smallest `prevLastRef + k*window` >= now.
//
//	retryAfter = rl.window - (now - prevLastRef) % rl.window
//
// clamped to (0, rl.window]. The integer-second coercion (Ceil + floor
// at 1s) lives in the handler so the limiter stays RFC-agnostic and
// the wire-shape decision stays in the transport layer (godlike/06
// SSOT: one canonical owner per concern).
//
// FWD-POINTER (godlike/07 honest-limitation): the current scheme
// unconditionally updates `b.lastRef = now` even when the call is
// denied, which in the integer-window scheme makes `elapsed / window`
// accumulate zero across rapid-fire denied requests until the caller
// pauses for a full `rl.window`. The honest Retry-After value
// therefore equals `rl.window` for denied requests issued immediately
// after exhaustion. Tightening the refill to a smooth-per-token
// scheme is OUT OF SCOPE for this commit (AGENTS.md "Do not add
// features unless explicitly requested"; godlike/06 minimum-blast-
// radius); track as a separate PR if/when needed.
func (rl *tokenBucketRateLimiter) Allow(key string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{tokens: rl.limit - 1, lastRef: now}
		rl.buckets[key] = b
		return true, 0
	}

	prevLastRef := b.lastRef
	elapsed := now.Sub(prevLastRef)
	refill := int(elapsed / rl.window * time.Duration(rl.limit))
	if refill > 0 {
		b.tokens += refill
		if b.tokens > rl.limit {
			b.tokens = rl.limit
		}
	}
	b.lastRef = now

	if b.tokens <= 0 {
		// Reuse `elapsed` instead of re-computing now.Sub(prevLastRef):
		// one critical section, no extra time arithmetic on the hot path.
		retryAfter := rl.window - (elapsed % rl.window)
		if retryAfter <= 0 {
			retryAfter = rl.window
		}
		return false, retryAfter
	}
	b.tokens--
	return true, 0
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

// RateLimitMiddleware holds the middleware and its associated rate limiter.
type RateLimitMiddleware struct {
	Handler gin.HandlerFunc
	limiter *tokenBucketRateLimiter
}

// Stop signals the rate limiter's cleanup goroutine to terminate.
func (r *RateLimitMiddleware) Stop() {
	if r.limiter != nil {
		r.limiter.Stop()
	}
}

// RateLimit creates a rate limiting middleware.
//
// PG-006 (June 2026): the previous signature took *config.Config. The
// middleware now consumes middleware.RateLimitPort (defined in
// internal/application/middleware/ports.go). Concrete adapter lives
// at internal/app/middleware_security_adapter.go. The returned
// RateLimitMiddleware must have its Stop() method called during
// server shutdown to prevent goroutine leaks.
func RateLimit(rl middleware.RateLimitPort, env EnvReader) *RateLimitMiddleware {
	if rl == nil || !rl.RateLimitEnabled() {
		return &RateLimitMiddleware{
			Handler: func(c *gin.Context) {
				// RateLimit disabled ctor: voiceover bypass is irrelevant
				// because no limiter is configured in the first place.
				c.Next()
			},
		}
	}

	bypassVoiceoverBurst := voiceoverBurstBypassEnabled(env)

	limiter := newTokenBucketRateLimiter(rl.RateLimitRequests(), time.Minute)

	go limiter.Cleanup()

	return &RateLimitMiddleware{
		Handler: func(c *gin.Context) {
			// Step[8] / Fail-mode 429: VELOX_VOICEOVER_RATE_LIMIT_BURST=1
			// bypasses the per-IP bucket ONLY for /api/media/voiceover/*.
			// Production leaves the env unset; the E2E test runners
			// (Steps [3]+[6] multi-folder loop) opt in to burst the
			// voiceover POST without weakening the global rate limit
			// (godlike/07 minimum-blast-radius).
			if bypassVoiceoverBurst && isVoiceoverBurstBypassRoute(c) {
				c.Next()
				return
			}
			key := c.ClientIP()
			allowed, retryAfter := limiter.Allow(key)
			if !allowed {
				// godlike/07 fail-closed: emit the HONEST time until the
				// limiter would admit one token (not a generic placeholder).
				// RFC 7231 §7.1.3: delta-seconds is a non-negative integer.
				// Floor to 1s so the header can never advise "retry now"
				// (which would defeat the limiter — the caller would spin
				// on the same 429 in a tight loop).
				secs := int(math.Ceil(retryAfter.Seconds()))
				if secs < 1 {
					secs = 1
				}
				c.Header("Retry-After", strconv.Itoa(secs))
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
