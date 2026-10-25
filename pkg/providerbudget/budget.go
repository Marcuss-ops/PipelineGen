// Package providerbudget bounds outbound retrieval calls per provider.
//
// A retrieval batch fans out over several independent upstreams, and each
// upstream has its own tolerance: Wikimedia Commons starts answering HTTP 429
// after a handful of concurrent queries, DuckDuckGo challenges an IP after a
// burst, and SearXNG is a local process with yet another ceiling. A single
// global concurrency limiter cannot express those different budgets, and a
// naive goroutine fan-out turns a fast batch into a fast wall clock with an
// empty result set.
//
// Registry is one token bucket per provider:
//
//   - Interval is the minimum spacing between two call starts (0 disables
//     pacing for providers that already own their limiter).
//   - Burst is how many permits may accumulate while the lane is idle.
//   - Cooldown is the lane-wide pause applied after a rate-limit response.
//
// Acquire blocks until a permit is available or the context ends. A provider
// that answers 429 calls ObserveRateLimit, which pauses the whole lane for the
// Retry-After window (or the configured cooldown) instead of letting the next
// worker pay for the same refusal.
package providerbudget

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// DefaultCooldown is used when a rate-limit response carries no Retry-After
// and the lane has no explicit cooldown.
const DefaultCooldown = 30 * time.Second

// Config is the budget of one provider lane.
type Config struct {
	// Interval is the minimum spacing between two call starts. Zero disables
	// pacing: the lane still enforces Burst and Cooldown.
	Interval time.Duration
	// Burst is the maximum number of accumulated permits. Values below one
	// are clamped to one.
	Burst int
	// Cooldown is the pause applied after a rate-limit response that carries
	// no usable Retry-After. Zero falls back to DefaultCooldown.
	Cooldown time.Duration
}

func (c Config) normalized() Config {
	if c.Burst < 1 {
		c.Burst = 1
	}
	if c.Cooldown <= 0 {
		c.Cooldown = DefaultCooldown
	}
	if c.Interval < 0 {
		c.Interval = 0
	}
	return c
}

// Snapshot is the observable state of one lane. It is a measurement, never a
// control surface: callers must use Acquire/ObserveRateLimit.
type Snapshot struct {
	Provider     string
	Interval     time.Duration
	Burst        int
	Acquisitions int
	Waits        int
	Remaining    time.Duration
}

type laneState struct {
	cfg           Config
	tokens        float64
	last          time.Time
	cooldownUntil time.Time
	acquisitions  int
	waits         int
}

// Registry owns one token bucket per provider. It is safe for concurrent use.
type Registry struct {
	mu       sync.Mutex
	defaults Config
	lanes    map[string]*laneState
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
}

// New builds a registry whose explicit lanes start from defaults.
func New(defaults Config) *Registry {
	return &Registry{
		defaults: defaults.normalized(),
		lanes:    make(map[string]*laneState),
		now:      time.Now,
		sleep:    sleepContext,
	}
}

// Configure installs an explicit budget for one provider. It must be called
// before the lane is used; configuring a live lane keeps its consumed permits
// and only replaces the rate parameters.
func (r *Registry) Configure(provider string, cfg Config) {
	if r == nil || provider == "" {
		return
	}
	cfg = cfg.normalized()
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.lanes[provider]
	if st == nil {
		r.lanes[provider] = &laneState{cfg: cfg, tokens: float64(cfg.Burst), last: r.now()}
		return
	}
	st.cfg = cfg
	if st.tokens > float64(cfg.Burst) {
		st.tokens = float64(cfg.Burst)
	}
}

func (r *Registry) lane(provider string) *laneState {
	st := r.lanes[provider]
	if st == nil {
		st = &laneState{cfg: r.defaults, tokens: float64(r.defaults.Burst), last: r.now()}
		r.lanes[provider] = st
	}
	return st
}

// Acquire blocks until the provider lane grants one permit. Pacing uses the
// lane's token bucket; a lane in cooldown waits for the cooldown to expire
// even when a token is available, because the upstream is refusing traffic.
func (r *Registry) Acquire(ctx context.Context, provider string) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		wait := r.reserve(provider)
		if wait <= 0 {
			return nil
		}
		if err := r.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// reserve consumes a permit or returns how long the caller must wait.
func (r *Registry) reserve(provider string) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	st := r.lane(provider)
	if now.Before(st.cooldownUntil) {
		st.waits++
		return st.cooldownUntil.Sub(now)
	}
	if st.cfg.Interval <= 0 {
		st.acquisitions++
		return 0
	}
	elapsed := now.Sub(st.last)
	if elapsed > 0 {
		st.tokens = math.Min(float64(st.cfg.Burst), st.tokens+float64(elapsed)/float64(st.cfg.Interval))
		st.last = now
	}
	if st.tokens >= 1 {
		st.tokens--
		st.acquisitions++
		return 0
	}
	st.waits++
	deficit := 1 - st.tokens
	return time.Duration(deficit * float64(st.cfg.Interval))
}

// ObserveRateLimit pauses the lane for retryAfter (or the configured cooldown
// when retryAfter is not positive). The longest pending pause wins, so a late
// short Retry-After cannot shorten an already open window.
func (r *Registry) ObserveRateLimit(provider string, retryAfter time.Duration) {
	if r == nil || provider == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.lane(provider)
	if retryAfter <= 0 {
		retryAfter = st.cfg.Cooldown
	}
	if retryAfter <= 0 {
		retryAfter = DefaultCooldown
	}
	until := r.now().Add(retryAfter)
	if until.After(st.cooldownUntil) {
		st.cooldownUntil = until
	}
}

// Remaining reports how long the lane is paused or how long until its next
// permit, whichever is longer. Zero means a permit is available now.
func (r *Registry) Remaining(provider string) time.Duration {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	st := r.lane(provider)
	if now.Before(st.cooldownUntil) {
		return st.cooldownUntil.Sub(now)
	}
	if st.cfg.Interval <= 0 || st.tokens >= 1 {
		return 0
	}
	return time.Duration((1 - st.tokens) * float64(st.cfg.Interval))
}

// Snapshot returns every known lane in a deterministic order.
func (r *Registry) Snapshot() []Snapshot {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Snapshot, 0, len(r.lanes))
	for provider, st := range r.lanes {
		out = append(out, Snapshot{
			Provider: provider, Interval: st.cfg.Interval, Burst: st.cfg.Burst,
			Acquisitions: st.acquisitions, Waits: st.waits,
			Remaining: r.remainingLocked(st, r.now()),
		})
	}
	sortSnapshots(out)
	return out
}

func (r *Registry) remainingLocked(st *laneState, now time.Time) time.Duration {
	if now.Before(st.cooldownUntil) {
		return st.cooldownUntil.Sub(now)
	}
	if st.cfg.Interval <= 0 || st.tokens >= 1 {
		return 0
	}
	return time.Duration((1 - st.tokens) * float64(st.cfg.Interval))
}

// sortSnapshots keeps Snapshot deterministic without importing sort into the
// hot path above.
func sortSnapshots(out []Snapshot) {
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Provider < out[j-1].Provider; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
}

// RateLimited is the typed refusal a retrieval port returns for HTTP 429 (or
// an equivalent provider throttle). The scheduler uses it to open the provider
// lane cooldown and retry the same rung instead of treating the refusal as a
// search miss.
type RateLimited struct {
	Provider   string
	RetryAfter time.Duration
}

func (e *RateLimited) Error() string {
	if e == nil {
		return "provider rate limited"
	}
	if e.Provider == "" {
		return fmt.Sprintf("provider rate limited (retry after %s)", e.RetryAfter)
	}
	return fmt.Sprintf("%s rate limited (retry after %s)", e.Provider, e.RetryAfter)
}

// AsRateLimited recovers the typed refusal through any wrapping.
func AsRateLimited(err error) (*RateLimited, bool) {
	var limited *RateLimited
	if errors.As(err, &limited) {
		return limited, true
	}
	return nil, false
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
