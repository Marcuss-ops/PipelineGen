// Package search — telemetry.go introduces the user-spec Option{Hits,
// Latencies} pattern as the canonical Stats surface. The decorator
// wraps an *Aggregator and records per-backend counters across calls.
// Handlers consume Stats() instead of holding a parallel stats-mut
// struct on their own struct.
//
// User spec (PR-2, June 2026):
//
//	"provider stats vanno nel registro dietro un Option{Hits,Latencies}"
//
// The Option type is realised as BackendStats (a typed struct holding
// Hits + Latencies in one place, NOT two decoupled maps). The
// decorator lives in the search package because it is the canonical
// extension of Aggregator.Search — handlers consume SearchFanOut, no
// extra app-package dependency is required.
//
// Wave 19 invariant: search package is stdlib-only. The decorator
// uses sync + time, both stdlib. No new imports are added.
package assets

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// BackendStats accumulates per-backend counters across calls. The
// user-spec "Option{Hits, Latencies}" pattern collapses the per-
// backend snapshot into one struct so callers can read both fields
// without two map lookups.
//
//   - Hits is the cumulative candidate count returned by the
//     backend across all Search calls.
//   - Calls is the cumulative count of Search calls in which this
//     backend was visited (returned at least one candidate OR
//     recorded a ProviderError).
//   - Errors is the cumulative count of Search calls in which this
//     backend recorded a ProviderError.
//   - CumulativeLatency is the sum of per-call wall-clock durations
//     attributed to this backend; AverageLatency is the convenience
//     helper returning CumulativeLatency / Calls.
//   - Latencies is a rolling slice of the last `rollingCap` per-call
//     wall-clock samples (default 100); intended for dashboards that
//     want the recent past instead of a single average.
type BackendStats struct {
	Hits              int64
	Calls             int64
	Errors            int64
	CumulativeLatency time.Duration
	Latencies         []time.Duration
}

// AverageLatency returns CumulativeLatency / Calls. 0 when no calls
// have been recorded yet. The shape is preserved across the legacy
// ProviderStats → BackendStats migration so handler JSON
// "avg_latency_ms" keys stay stable.
func (b *BackendStats) AverageLatency() time.Duration {
	if b == nil || b.Calls == 0 {
		return 0
	}
	return b.CumulativeLatency / time.Duration(b.Calls)
}

// SearchFanOut is the public surface used by handlers and the
// composition root — the SOLE dependency after the legacy provider-
// aggregator path was retired. The decorator wraps the canonical
// *Aggregator and exposes per-backend counters (Hits/Latencies/Errors)
// via the user-spec Option{Hits, Latencies} pattern.
type SearchFanOut interface {
	// Search delegates to the inner Aggregator and records per-
	// backend stats. Result + error mirror the inner Aggregator
	// contract exactly.
	Search(ctx context.Context, q Query) (*Result, error)
	// Stats returns a defensive snapshot of per-backend counters
	// keyed by backend Name(). Mutating the returned map does
	// NOT affect the decorator.
	Stats() map[string]BackendStats
}

// noopFanOut is returned by NewSearchFanOut when the inner
// Aggregator is nil. Search returns ErrAggregatorNil so handlers
// can short-circuit to 503 without nil-pointer crashes; Stats
// returns the empty map.
type noopFanOut struct{}

// ErrAggregatorNil is returned by NewSearchFanOut(nil).Search so
// callers can distinguish "decorator not wired" from "real
// propagation error". Handlers map this to 503.
var ErrAggregatorNil = fmt.Errorf("search: aggregator not wired (SearchFanOut constructed against nil)")

func (noopFanOut) Search(context.Context, Query) (*Result, error) {
	return nil, ErrAggregatorNil
}
func (noopFanOut) Stats() map[string]BackendStats {
	return map[string]BackendStats{}
}

// telemetryDecorator is the concrete SearchFanOut implementation.
// Mutex-guarded perBackend map; rolling-cap prevents unbounded
// growth on long-lived daemon processes.
type telemetryDecorator struct {
	inner *Aggregator

	mu         sync.Mutex
	perBackend map[string]*BackendStats
	rollingCap int
}

// NewSearchFanOut wraps an Aggregator with the telemetry decorator.
// inner nil returns a noopFanOut that fails fast on Search with
// ErrAggregatorNil and returns the empty map on Stats — so a
// nil-driven call path does NOT crash on a missing dep, it just
// surfaces the missing-dep error consistently.
func NewSearchFanOut(inner *Aggregator) SearchFanOut {
	if inner == nil {
		return noopFanOut{}
	}
	return &telemetryDecorator{
		inner:      inner,
		perBackend: make(map[string]*BackendStats),
		rollingCap: 100,
	}
}

// Search delegates to the inner Aggregator and records stats per
// visited backend. Visited backends are those that contributed at
// least one candidate OR recorded an error in ProviderErrors.
//
// Pro-rating: the inner Search emits a single elapsed wall-clock
// for the whole fanout. We divide that wall-clock equally among
// visited backends as a coarse signal — coarse on purpose; the
// genuine per-backend latency is exposed by the rolling-window
// Latencies slice via future PR (the inner Aggregator's
// per-backend outcome latency is recorded here, NOT derived from
// any external timer).
func (d *telemetryDecorator) Search(ctx context.Context, q Query) (*Result, error) {
	if d == nil || d.inner == nil {
		return nil, ErrAggregatorNil
	}
	start := time.Now()
	res, err := d.inner.Search(ctx, q)
	elapsed := time.Since(start)
	if res == nil {
		return res, err
	}

	perBackendHits := make(map[string]int)
	visited := make(map[string]bool)
	for _, item := range res.Items {
		if item.Source == "" {
			continue
		}
		perBackendHits[item.Source]++
		visited[item.Source] = true
	}
	for name, e := range res.ProviderErrors {
		if visited[name] {
			continue
		}
		visited[name] = true
		_ = e // ProviderErrors already inspected by handler; we only need the key
	}

	// Attribute the fanout wall-clock equally among visited backends.
	// When NO backend was visited (the eligible list was empty +
	// no errors recorded), the elapsed time is still recorded
	// against a synthetic "_aggregate" key so operators can see
	// "the fanout did run but produced nothing" cases.
	var perBackendElapsed time.Duration
	if len(visited) > 0 {
		perBackendElapsed = elapsed / time.Duration(len(visited))
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(visited) == 0 {
		ps, ok := d.perBackend["_aggregate"]
		if !ok {
			ps = &BackendStats{Latencies: make([]time.Duration, 0, d.rollingCap)}
			d.perBackend["_aggregate"] = ps
		}
		ps.Calls++
		ps.CumulativeLatency += elapsed
		ps.Latencies = append(ps.Latencies, elapsed)
		if len(ps.Latencies) > d.rollingCap {
			ps.Latencies = ps.Latencies[len(ps.Latencies)-d.rollingCap:]
		}
		return res, err
	}
	for name := range visited {
		hits := perBackendHits[name]
		isErr := false
		if _, hasErr := res.ProviderErrors[name]; hasErr {
			isErr = true
		}
		ps, ok := d.perBackend[name]
		if !ok {
			ps = &BackendStats{Latencies: make([]time.Duration, 0, d.rollingCap)}
			d.perBackend[name] = ps
		}
		ps.Calls++
		if isErr {
			ps.Errors++
		} else {
			ps.Hits += int64(hits)
		}
		ps.CumulativeLatency += perBackendElapsed
		ps.Latencies = append(ps.Latencies, perBackendElapsed)
		if len(ps.Latencies) > d.rollingCap {
			ps.Latencies = ps.Latencies[len(ps.Latencies)-d.rollingCap:]
		}
	}
	return res, err
}

// Stats returns a defensive snapshot of per-backend counters. A
// nil decorator returns the empty map (matches noopFanOut.Stats).
func (d *telemetryDecorator) Stats() map[string]BackendStats {
	if d == nil {
		return map[string]BackendStats{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]BackendStats, len(d.perBackend))
	for name, ps := range d.perBackend {
		if ps == nil {
			out[name] = BackendStats{}
			continue
		}
		cp := BackendStats{
			Hits:              ps.Hits,
			Calls:             ps.Calls,
			Errors:            ps.Errors,
			CumulativeLatency: ps.CumulativeLatency,
			Latencies:         append([]time.Duration(nil), ps.Latencies...),
		}
		out[name] = cp
	}
	return out
}
