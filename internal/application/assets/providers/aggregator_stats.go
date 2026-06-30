package providers

import (
	"time"
)

// ProviderStats is the per-provider live telemetry captured by the
// aggregator's Stats() surface. Updated under a mutex in each
// fan-out goroutine exit.
//
// ErrorRate = Errors / Calls (0 when Calls == 0).
// Latency is the cumulative wall-clock across all calls to the
// provider (average latency = Latency / Calls when Calls > 0).
// Hits is the cumulative candidate count returned across all calls.
type ProviderStats struct {
	Hits    int
	Calls   int
	Errors  int
	Latency time.Duration
}

// ErrorRate returns the rolling error rate as a fraction in [0, 1].
// Returns 0 when no calls have been recorded yet.
func (s *ProviderStats) ErrorRate() float64 {
	if s == nil || s.Calls == 0 {
		return 0
	}
	return float64(s.Errors) / float64(s.Calls)
}

// AvgLatency returns Latency / Calls. 0 when Calls == 0.
func (s *ProviderStats) AvgLatency() time.Duration {
	if s == nil || s.Calls == 0 {
		return 0
	}
	return s.Latency / time.Duration(s.Calls)
}

// AggregateStats is the snapshot returned by SearchAggregator.Stats().
// Providers is keyed by Provider.Name(); entries that were never
// called do NOT appear in the map (their absence signals
// "never invoked", matching HealthCheck's nil-probe convention).
type AggregateStats struct {
	Providers map[string]*ProviderStats
}

// providerOutcome is the per-goroutine result of one fan-out worker.
// Declared at file scope so recordOutcome and the goroutine body
// can both name it without an inline struct + type-alias dance.
type providerOutcome struct {
	hits    []ScoredHit
	err     error
	cursor  string
	latency time.Duration
}

// recordOutcome updates the per-provider stats counters atomically.
// Tolerant to nil aggregator so a hypothetical future caller can
// construct a stats-disabled aggregator without re-implementing the
// outcome-routing logic.
//
// name MUST be the canonical entry.Name (NOT the NextPageToken) so
// the stats map is keyed by provider identity. Goroutines pass
// entry.Name explicitly — past versions keyed the stats map by
// the cursor field, which was incorrect: NextPageToken is provider-
// opaque and re-using it as a stats key silently merged distinct
// providers' counters.
func (a *SearchAggregator) recordOutcome(name string, out providerOutcome) {
	if a == nil {
		return
	}
	if name == "" {
		// Defensive: an empty name usually means a registry
		// mutation race during fan-out. Skip stats for this entry
		// rather than synthesizing a fake key that would silently
		// merge into the map.
		return
	}
	a.statsMu.Lock()
	ps, ok := a.stats[name]
	if !ok {
		ps = &ProviderStats{}
		a.stats[name] = ps
	}
	ps.Calls++
	ps.Latency += out.latency
	if out.err != nil {
		ps.Errors++
	} else {
		ps.Hits += len(out.hits)
	}
	a.statsMu.Unlock()
}

// Stats returns a snapshot copy of the per-provider stats so callers
// can render dashboards without holding the aggregator's lock. The
// returned struct is decoupled from internal state — mutating it
// does not affect the aggregator.
func (a *SearchAggregator) Stats() AggregateStats {
	if a == nil {
		return AggregateStats{Providers: map[string]*ProviderStats{}}
	}
	a.statsMu.RLock()
	defer a.statsMu.RUnlock()
	out := AggregateStats{Providers: make(map[string]*ProviderStats, len(a.stats))}
	for name, ps := range a.stats {
		if ps == nil {
			out.Providers[name] = &ProviderStats{}
			continue
		}
		// Defensive copy: callers can mutate the snapshot
		// freely without affecting aggregator state.
		cp := *ps
		out.Providers[name] = &cp
	}
	return out
}
