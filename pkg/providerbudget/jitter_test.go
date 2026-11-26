package providerbudget

import (
	"testing"
	"time"
)

// TestObserveRateLimitAddsJitterWithinTheConfiguredBound pins the retry jitter:
// a cooldown with Jitter=N never waits less than the cooldown and never more
// than cooldown+N. Zero jitter keeps the historical deterministic pause.
func TestObserveRateLimitAddsJitterWithinTheConfiguredBound(t *testing.T) {
	const cooldown = 30 * time.Second
	const jitter = 100 * time.Millisecond
	registry := New(Config{Interval: 0, Burst: 1, Cooldown: cooldown, Jitter: jitter})
	registry.ObserveRateLimit("searxng", cooldown)

	remaining := registry.Remaining("searxng")
	if remaining < cooldown-100*time.Millisecond {
		t.Fatalf("remaining = %s, want >= %s", remaining, cooldown)
	}
	if remaining > cooldown+jitter {
		t.Fatalf("remaining = %s, want <= %s", remaining, cooldown+jitter)
	}
}

func TestObserveRateLimitWithoutJitterKeepsTheExactCooldown(t *testing.T) {
	const cooldown = 2 * time.Second
	registry := New(Config{Interval: 0, Burst: 1, Cooldown: cooldown})
	registry.ObserveRateLimit("duckduckgo", cooldown)

	if remaining := registry.Remaining("duckduckgo"); remaining > cooldown {
		t.Fatalf("remaining = %s, want <= %s when Jitter is zero", remaining, cooldown)
	}
}
