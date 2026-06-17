package monitor

import (
	"sync"
	"time"
)

// ── Token Bucket Rate Limiter ──────────────────────────────────────────────

// tokenBucket is a simple rate limiter that allows N operations per window.
// Thread-safe via mutex. Used to prevent exceeding YouTube search quota.
type tokenBucket struct {
	mu        sync.Mutex
	maxTokens int
	window    time.Duration
	tokens    int
	lastReset time.Time
}

// newTokenBucket creates a token bucket that allows maxTokens per window.
func newTokenBucket(maxTokens int, window time.Duration) *tokenBucket {
	return &tokenBucket{
		maxTokens: maxTokens,
		window:    window,
		tokens:    maxTokens,
		lastReset: time.Now(),
	}
}

// Allow consumes one token. Returns true if allowed, false if rate limited.
func (tb *tokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.maybeReset()
	if tb.tokens <= 0 {
		return false
	}
	tb.tokens--
	return true
}

// Remaining returns the number of tokens left in the current window.
func (tb *tokenBucket) Remaining() int {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.maybeReset()
	return tb.tokens
}

// ResetIn returns the duration until the next window reset.
func (tb *tokenBucket) ResetIn() time.Duration {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	elapsed := time.Since(tb.lastReset)
	if elapsed >= tb.window {
		return 0
	}
	return tb.window - elapsed
}

// maybeReset resets the tokens if the window has elapsed.
func (tb *tokenBucket) maybeReset() {
	if time.Since(tb.lastReset) >= tb.window {
		tb.tokens = tb.maxTokens
		tb.lastReset = time.Now()
	}
}
