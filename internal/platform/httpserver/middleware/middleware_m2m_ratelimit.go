package middleware

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// M2MClientRateLimiter applies the per-client token bucket stored in
// m2m_clients.rate_limit_rps / rate_limit_burst. One instance must be shared
// by every M2M route group: a limiter per group would let a client spend its
// full budget once on /api/v1/jobs and again on /api/v1/media.
type M2MClientRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*m2mTokenBucket
}

type m2mTokenBucket struct {
	tokens float64
	last   time.Time
}

func NewM2MClientRateLimiter() *M2MClientRateLimiter {
	return &M2MClientRateLimiter{buckets: make(map[string]*m2mTokenBucket)}
}

// Handler is the gin middleware. It must run after JobClientAuthMiddleware.
// In the dev/test pass-through mode there is no client row, so it is a no-op.
func (l *M2MClientRateLimiter) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		client, ok := M2MClientFromContext(c)
		if !ok {
			c.Next()
			return
		}

		rps := client.RateLimitRPS
		if rps <= 0 {
			rps = 2
		}
		burst := client.RateLimitBurst
		if burst <= 0 {
			burst = 10
		}

		key := client.ClientID
		if key == "" {
			key = "ip:" + c.ClientIP()
		}
		allowed, retryAfter := l.allow(key, rps, burst)
		if allowed {
			c.Next()
			return
		}

		secs := int(math.Ceil(retryAfter.Seconds()))
		if secs < 1 {
			secs = 1
		}
		c.Header("Retry-After", strconv.Itoa(secs))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"ok":             false,
			"error":          "m2m rate limit exceeded",
			"client_id":      client.ClientID,
			"rate_limit_rps": rps,
			"burst":          burst,
		})
		c.Abort()
	}
}

func (l *M2MClientRateLimiter) allow(key string, rps float64, burst int) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	bucket := l.buckets[key]
	if bucket == nil {
		bucket = &m2mTokenBucket{tokens: float64(burst), last: now}
		l.buckets[key] = bucket
	} else {
		elapsed := now.Sub(bucket.last).Seconds()
		bucket.tokens = math.Min(float64(burst), bucket.tokens+elapsed*rps)
		bucket.last = now
	}

	if bucket.tokens < 1 {
		need := (1 - bucket.tokens) / rps
		retryAfter := time.Duration(math.Ceil(need*1000)) * time.Millisecond
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		return false, retryAfter
	}

	bucket.tokens--
	return true, 0
}

// M2MDownloadConcurrency caps simultaneous downloads per M2M client. The
// catalog endpoints stay responsive while one client cannot occupy every
// download slot on the master. A non-blocking rejection is deliberate: a
// queued download would hold an HTTP connection without doing work.
func M2MDownloadConcurrency(max int) gin.HandlerFunc {
	if max <= 0 {
		max = 2
	}

	var mu sync.Mutex
	gates := make(map[string]chan struct{})

	return func(c *gin.Context) {
		client, ok := M2MClientFromContext(c)
		if !ok {
			c.Next()
			return
		}
		key := client.ClientID
		if key == "" {
			key = "ip:" + c.ClientIP()
		}

		mu.Lock()
		gate := gates[key]
		if gate == nil {
			gate = make(chan struct{}, max)
			gates[key] = gate
		}
		mu.Unlock()

		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
			c.Next()
		default:
			c.Header("Retry-After", "1")
			c.JSON(http.StatusTooManyRequests, gin.H{
				"ok":        false,
				"error":     "too many concurrent downloads",
				"client_id": client.ClientID,
				"max":       max,
			})
			c.Abort()
		}
	}
}
