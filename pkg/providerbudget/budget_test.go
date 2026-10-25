package providerbudget

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return nil
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestRegistry(defaults Config) (*Registry, *fakeClock) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	registry := New(defaults)
	registry.now = clock.Now
	registry.sleep = clock.Sleep
	return registry, clock
}

func TestAcquireEnforcesBurstThenInterval(t *testing.T) {
	registry, clock := newTestRegistry(Config{Interval: time.Second, Burst: 2, Cooldown: time.Minute})
	registry.Configure("commons", Config{Interval: time.Second, Burst: 2, Cooldown: time.Minute})

	for i := 0; i < 2; i++ {
		if err := registry.Acquire(context.Background(), "commons"); err != nil {
			t.Fatalf("burst acquire %d: %v", i, err)
		}
	}
	if got := clock.Now(); !got.Equal(time.Unix(1000, 0)) {
		t.Fatalf("burst acquires must not wait, clock moved to %s", got)
	}
	if err := registry.Acquire(context.Background(), "commons"); err != nil {
		t.Fatalf("third acquire: %v", err)
	}
	if got := clock.Now(); !got.Equal(time.Unix(1001, 0)) {
		t.Fatalf("third acquire waited to %s, want one interval", got)
	}
	snapshots := registry.Snapshot()
	if len(snapshots) != 1 || snapshots[0].Acquisitions != 3 || snapshots[0].Waits != 1 {
		t.Fatalf("snapshot = %+v, want 3 acquisitions and 1 wait", snapshots)
	}
}

func TestObserveRateLimitPausesTheWholeLane(t *testing.T) {
	registry, clock := newTestRegistry(Config{Interval: time.Millisecond, Burst: 1, Cooldown: 10 * time.Second})
	registry.Configure("duckduckgo", Config{Interval: time.Millisecond, Burst: 1, Cooldown: 10 * time.Second})

	registry.ObserveRateLimit("duckduckgo", 7*time.Second)
	if remaining := registry.Remaining("duckduckgo"); remaining != 7*time.Second {
		t.Fatalf("remaining = %s, want 7s", remaining)
	}
	if err := registry.Acquire(context.Background(), "duckduckgo"); err != nil {
		t.Fatalf("acquire after cooldown: %v", err)
	}
	if got := clock.Now(); !got.Equal(time.Unix(1007, 0)) {
		t.Fatalf("acquire waited to %s, want the 7s Retry-After", got)
	}

	// A later, shorter refusal must not shorten an open window.
	registry.ObserveRateLimit("duckduckgo", 20*time.Second)
	registry.ObserveRateLimit("duckduckgo", time.Second)
	if remaining := registry.Remaining("duckduckgo"); remaining < 19*time.Second {
		t.Fatalf("short Retry-After shortened the window: %s", remaining)
	}
}

func TestLanesAreIndependent(t *testing.T) {
	registry, _ := newTestRegistry(Config{Interval: time.Second, Burst: 1})
	registry.Configure("slow", Config{Interval: time.Hour, Burst: 1})
	registry.Configure("fast", Config{Interval: 0, Burst: 1})

	if err := registry.Acquire(context.Background(), "slow"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Acquire(context.Background(), "fast"); err != nil {
		t.Fatalf("fast lane must not wait for slow lane: %v", err)
	}
	if remaining := registry.Remaining("fast"); remaining != 0 {
		t.Fatalf("fast lane remaining = %s, want 0", remaining)
	}
}

func TestAcquireHonorsCancellation(t *testing.T) {
	registry, _ := newTestRegistry(Config{Interval: time.Hour, Burst: 1})
	registry.Configure("commons", Config{Interval: time.Hour, Burst: 1})
	if err := registry.Acquire(context.Background(), "commons"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := registry.Acquire(ctx, "commons"); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire error = %v, want context.Canceled", err)
	}
}

func TestRateLimitedIsRecoverableThroughWrapping(t *testing.T) {
	limited := &RateLimited{Provider: "commons", RetryAfter: 4 * time.Second}
	wrapped := fmt.Errorf("semantic search: %w", limited)
	got, ok := AsRateLimited(wrapped)
	if !ok || got.RetryAfter != 4*time.Second || got.Provider != "commons" {
		t.Fatalf("AsRateLimited = %+v, %v", got, ok)
	}
	if _, ok := AsRateLimited(errors.New("boom")); ok {
		t.Fatal("a plain error must not be a rate limit")
	}
}
