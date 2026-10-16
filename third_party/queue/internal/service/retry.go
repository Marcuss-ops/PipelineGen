package service

import (
	"math/rand/v2"
	"time"
)

// RetryConfig controls how RequeueExpired retries transient failures.
//
// The zero value performs a single attempt with no retry, preserving the
// previous behavior. Set MaxAttempts > 1 to enable retries: each retry sleeps
// BaseDelay * 2^(n-1) (capped at MaxDelay) with uniform jitter.
type RetryConfig struct {
	// MaxAttempts is the total number of attempts, including the first.
	// A value <= 1 disables retries.
	MaxAttempts int

	// BaseDelay is the sleep before the first retry; it doubles each retry.
	BaseDelay time.Duration

	// MaxDelay caps the exponential backoff. Zero means no cap.
	MaxDelay time.Duration

	// Jitter is the fraction of the delay randomized uniformly. It is clamped
	// to [0,1]: 0 disables jitter, 1 randomizes over the full [0, delay] range.
	Jitter float64
}

// defaultBaseDelay is used when retries are enabled but BaseDelay is unset.
const defaultBaseDelay = 100 * time.Millisecond

// backoffDelay returns the sleep before retry number `failed` (the 1-indexed
// count of failed attempts so far). It doubles BaseDelay per failure, caps at
// MaxDelay and applies uniform jitter scaled by Jitter.
func backoffDelay(cfg RetryConfig, failed int) time.Duration {
	delay := cfg.BaseDelay
	if delay <= 0 {
		delay = defaultBaseDelay
	}
	for i := 1; i < failed; i++ {
		delay *= 2
		if cfg.MaxDelay > 0 && delay >= cfg.MaxDelay {
			delay = cfg.MaxDelay
			break
		}
	}

	if jitter := cfg.Jitter; jitter > 0 {
		if jitter > 1 {
			jitter = 1
		}
		// Scale the delay down by a uniform factor in [1-jitter, 1], so the
		// result stays within [delay*(1-jitter), delay].
		factor := 1 - jitter*rand.Float64()
		delay = time.Duration(float64(delay) * factor)
	}
	return delay
}
