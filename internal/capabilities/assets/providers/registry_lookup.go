package assets

import (
	"context"
	"sort"
	"sync"
)

// Get returns the provider with the given Name, or (nil, false) if
// absent.
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok {
		return nil, false
	}
	return e.Provider, true
}

// GetEntry returns the canonical ProviderEntry for the given name,
// or (nil, false) if absent. New S3a canonical access path for
// surfaces that need the typed capability set + probe + limits
// (e.g. HealthCheck).
func (r *Registry) GetEntry(name string) (*ProviderEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok || e == nil {
		return nil, false
	}
	return e, true
}

// ByCapability returns every registered provider advertising the
// given capability, sorted by Name() for deterministic iteration.
func (r *Registry) ByCapability(cap Capability) []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.entries))
	for _, e := range r.entries {
		// Migration: capability membership is read from the typed
		// ProviderCapabilitySet pointer fields; we treat a non-nil
		// pointer (or an entry whose original Provider still lists
		// the cap) as "advertised". The provider.Capabilities() path
		// remains the source of truth at the provider interface; the
		// typed pointer is the runtime enrichment.
		advertises := e.Capabilities.Has(cap)
		if !advertises && e.Provider != nil {
			for _, c := range e.Provider.Capabilities() {
				if c == cap {
					advertises = true
					break
				}
			}
		}
		if advertises {
			out = append(out, e.Provider)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// All returns every registered provider, sorted by Name() for
// deterministic iteration.
func (r *Registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.entries))
	for _, e := range r.entries {
		if e == nil {
			continue
		}
		out = append(out, e.Provider)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Entries returns every registered ProviderEntry, sorted by Name()
// for deterministic iteration. New S3a canonical accessor; matched
// by GetEntry for single-name lookups.
func (r *Registry) Entries() []*ProviderEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ProviderEntry, 0, len(r.entries))
	for _, e := range r.entries {
		if e == nil {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// IsFrozen reports whether the registry has been frozen.
func (r *Registry) IsFrozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

// HealthCheck runs every ProviderEntry's HealthProbe concurrently
// with bounded concurrency (semaphore size 4 per S3a spec). Each
// probe runs with `min(parentCtx, entry.HealthTimeout())` so a
// per-entry override beats the 5s default but never exceeds the
// caller's context.
//
// Returns a map[name]error. Semantics:
//   - Providers WITHOUT a HealthProbe are OMITTED (no map entry).
//     Map absence is the canonical signal for "provider does not
//     advertise health"; this is consistent with the typed
//     ProviderCapabilitySet's nil-pointer convention.
//   - A successful probe is recorded as `map[name]error = nil` so
//     dashboards can render "registered+probed+ok" as a single
//     diff against the registry's `len(Entries())`.
//
// First-error-wins is NOT the contract here — each probe is
// independent and aggregated into the result map. The bounded
// concurrency (4) caps the wall-clock cost; the per-entry timeout
// caps the per-probe cost. Operations that need fail-fast can
// observe completed probes via the map and abort their follow-up
// reads locally.
func (r *Registry) HealthCheck(ctx context.Context) map[string]error {
	if r == nil {
		return map[string]error{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Snapshot under RLock so the probes below run with no contention
	// on the registry mutex.
	entries := r.Entries()
	const maxConcurrent = 4
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	out := make(map[string]error, len(entries))
	var outMu sync.Mutex

	for _, e := range entries {
		if e == nil || e.HealthProbe == nil {
			// Skip: provider did not advertise a health probe. Absence
			// in the result map signals "no probe configured".
			continue
		}
		wg.Add(1)
		go func(entry *ProviderEntry) {
			defer wg.Done()
			// Acquire a semaphore slot before any blocking work so
			// the parallel budget is honoured even when probes take
			// their full timeout.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				outMu.Lock()
				out[entry.Name] = ctx.Err()
				outMu.Unlock()
				return
			}
			probeCtx, cancel := context.WithTimeout(ctx, entry.HealthTimeout())
			defer cancel()
			err := entry.HealthProbe(probeCtx)
			outMu.Lock()
			out[entry.Name] = err
			outMu.Unlock()
		}(e)
	}
	wg.Wait()
	return out
}

// RegisterSearch is a typed helper that accepts a SearchProvider
// and forwards it to Register. The compile-time interface check
// guarantees p.Search is implemented; intent at wiring time is
// also clearer ("this slot is a search slot").
//
// Use this over Register(p) whenever the adapter is guaranteed
// search-only — the registry's runtime behaviour is identical
// (filter via ByCapability(CapabilitySearch)), but the call site
// is self-documenting.
func (r *Registry) RegisterSearch(p SearchProvider) error {
	return r.Register(p)
}

// RegisterFetch is a typed helper that accepts a FetchProvider
// and forwards it to Register. The compile-time interface check
// guarantees p.Fetch is implemented.
//
// No adapter implements FetchProvider as of this commit; the
// helper exists so the Stock Provider PR can wire
// RegisterFetch(...) without introducing a new registry API.
func (r *Registry) RegisterFetch(p FetchProvider) error {
	return r.Register(p)
}
