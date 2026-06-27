package providers

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"
)

// Sentinel errors for the registry.
var (
	// ErrAlreadyRegistered is returned when a provider with the
	// same Name is already present. The returned error is wrapped
	// with %w so errors.Is(err, ErrAlreadyRegistered) matches.
	ErrAlreadyRegistered = errors.New("providers: provider already registered")

	// ErrFrozen is returned when Register is called after Freeze.
	ErrFrozen = errors.New("providers: registry frozen")

	// ErrNilProvider is returned when Register is called with a nil
	// Provider interface value.
	ErrNilProvider = errors.New("providers: nil provider")

	// ErrEmptyName is returned when Register is called with a
	// provider whose Name() returns "".
	ErrEmptyName = errors.New("providers: provider name is empty")

	// ErrNilEntry is returned when RegisterEntry is called with a
	// nil ProviderEntry pointer.
	ErrNilEntry = errors.New("providers: nil provider entry")
)

// DefaultHealthTimeout is the timeout applied to Provider-level
// health probes when the entry's Timeout field is left at its zero
// value. Per S3a (June 2026) spec: "timeout config (default 5s)".
const DefaultHealthTimeout = 5 * time.Second

// HealthProbe is the canonical health-check signature for a
// Provider. A nil HealthProbe on a ProviderEntry signals that the
// provider does not advertise health; HealthCheck() then omits the
// provider from its result map (no entry → no probe configured).
type HealthProbe func(ctx context.Context) error

// ProviderCapabilityDetail is the per-capability detail block.
//
// S3a (June 2026): this type is an open extension point. Today it
// carries tagged identity (so callers can detect "this capability
// has a non-nil detail pointer"). Future S3b-S3d waves will add
// the per-capability fields (e.g. PerResultLimitPerSource on Search,
// PerPageByteBudget on Fetch). To remain forward-compatible, all
// existing call sites MUST:
//   - read via `if det := entry.Capabilities.Search; det != nil`;
//   - write via direct field assignment on a *ProviderEntry (preferred)
//     OR by setting the per-capability detail pointer from a helper.
type ProviderCapabilityDetail struct {
	// Reserved for future per-capability metadata. The marker struct
	// is intentional: typed access (Option A) keeps
	// ProviderCapabilitySet ergonomics at O(1) and avoids map
	// allocations on the hot path (All/ByCapability/HealthCheck).
}

// ProviderCapabilitySet is the typed, per-capability map attached to
// a ProviderEntry. Migrated from the prior `[]Capability` shape via
// the EXPAND phase of godlike/07 (zero-legacy policy): existing
// adapters continue to expose `Capabilities() []Capability` on the
// Provider interface; the registry normalises those into the typed
// ProviderCapabilitySet once at Register time.
//
// Each pointer is nil when the provider does NOT advertise that
// capability, OR when the caller has not enriched the entry with a
// capability-specific detail pointer (zero-default path).
// CapabilitySet.Has(cap) reports truthiness for the specified cap.
//
// Rationale for Option A (typed pointers) vs Option B (map):
//   - O(1) typed-field reads on the hot path (HealthCheck /
//     ByCapability);
//   - compile-time enforcement when future capabilities are added
//     (new capability → add field → adapters compile-fail until
//     refreshed, vs map silently returning zero value);
//   - canonical in-tree clarity (no `for k, v := range caps` hot loops);
//   - forward-extension is a struct-field-add (one line, not a map
//     re-key). Trade-off: a new tag added at Provider-level requires
//     a ProviderCapabilitySet field add; documented in the package
//     doc so future maintainers understand the design choice.
type ProviderCapabilitySet struct {
	Search *ProviderCapabilityDetail
	Fetch  *ProviderCapabilityDetail
	Video  *ProviderCapabilityDetail
	Image  *ProviderCapabilityDetail
	Music  *ProviderCapabilityDetail
	Voice  *ProviderCapabilityDetail
	Script *ProviderCapabilityDetail
}

// Detail returns the per-capability detail pointer (or nil when the
// cap is unknown / not advertised by the set). Pointer return lets
// callers stamp in-place via `*caps.Detail(CapabilitySearch) = &Detail{}`.
func (s *ProviderCapabilitySet) Detail(cap Capability) *ProviderCapabilityDetail {
	switch cap {
	case CapabilitySearch:
		return s.Search
	case CapabilityFetch:
		return s.Fetch
	case CapabilityVideo:
		return s.Video
	case CapabilityImage:
		return s.Image
	case CapabilityMusic:
		return s.Music
	case CapabilityVoice:
		return s.Voice
	case CapabilityScript:
		return s.Script
	default:
		return nil
	}
}

// Has reports whether the set carries a non-nil detail pointer for
// the given capability. Used by callers that want a quick "does this
// provider support X with enriched metadata?" check without iterating.
func (s *ProviderCapabilitySet) Has(cap Capability) bool {
	return s.Detail(cap) != nil
}

// ProviderEntry is the full per-provider record held by the
// registry. S3a (June 2026) replaces the previous
// `map[string]Provider` storage with `map[string]*ProviderEntry` to
// carry the typed capability set + provider-level defaults (timeout,
// limits, health probe) without forcing every call site to thread an
// additional map[Capability]Detail alongside the provider lookups.
//
// Field semantics:
//   - Name         : canonical human-readable identifier, matches
//     Provider.Name() (the registry uses Name as the
//     dedup key).
//   - Provider     : the underlying source integration. Required.
//     Lookups via All/ByCapability/Get strip this and
//     return Provider for back-compat.
//   - Capabilities : typed set declared at Register time. Pointer
//     fields are populated when the provider advertises
//     the capability AND the caller has enriched the
//     entry with a per-capability detail. Zero value
//     (all nil) is acceptable and means "no per-
//     capability enrichment declared" — this is the
//     migration-friendly path from the previous
//     `[]Capability` shape.
//   - HealthProbe  : optional liveness probe for this provider. When
//     nil, Registry.HealthCheck omits the provider from
//     the result map (no entry = no probe configured).
//   - Timeout      : per-provider timeout applied to the HealthProbe
//     call. 0 falls back to DefaultHealthTimeout (5s).
//     Per S3a spec wording.
//   - MaxResults   : per-provider cap on candidate counts. 0 means
//     "use adapter default". Forwarded to consumers
//     in a future wave that pipes registry-level
//     limits into SearchRequest.Limit.
//   - MaxPages     : per-provider cap on pagination depth. 0 means
//     "no pagination" / adapter default. Same forward-
//     shape as MaxResults.
type ProviderEntry struct {
	Name         string
	Provider     Provider
	Capabilities ProviderCapabilitySet
	HealthProbe  HealthProbe
	Timeout      time.Duration
	MaxResults   int
	MaxPages     int
}

// HealthTimeout returns the effective probe timeout for this entry:
// the entry's Timeout unless zero, in which case DefaultHealthTimeout.
// Kept on the entry (not on Registry) so per-entry overrides win
// without registry-level state mutation.
func (e *ProviderEntry) HealthTimeout() time.Duration {
	if e == nil || e.Timeout <= 0 {
		return DefaultHealthTimeout
	}
	return e.Timeout
}

// Registry is a one-shot, freezeable provider catalog.
// Register/Freeze run once during composition root wiring; after
// Freeze() any call to Register() returns ErrFrozen.
//
// S3a (June 2026): storage shape migrated from
// `entries map[string]Provider` to `entries map[string]*ProviderEntry`.
// All lookup methods were updated to return Provider (stripping the
// Entry wrapper) so the existing public API surface is bit-for-bit
// preserved at the lookup-site. New code paths (HealthCheck +
// Entries + RegisterEntry) operate on the Entry surface directly.
//
// Concurrency: writes use sync.RWMutex.Lock; lookups
// (Get, All, ByCapability, IsFrozen, Entries) use RLock and are
// effectively wait-free after Freeze. Freeze is naturally idempotent
// — concurrent calls converge on the same final state with no data
// dependency.
//
// Determinism: All(), ByCapability(), and Entries() return slices
// sorted by Name() so callers can rely on a stable iteration order.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*ProviderEntry
	frozen  bool
}

// NewRegistry returns an empty, mutable registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]*ProviderEntry)}
}

// Register adds a provider under its Name(). The entry's Capabilities
// are populated from p.Capabilities() ([]Capability) — the typed
// ProviderCapabilitySet pointers are filled when the corresponding
// tag is advertised by the provider. HealthProbe / Timeout /
// MaxResults / MaxPages left zero-valued by default (zero-defaults
// per S3a spec wording "zero-value defaults").
//
// Returns:
//   - ErrNilProvider        if p is the zero Provider value;
//   - ErrEmptyName          if p.Name() returns "";
//   - ErrFrozen             if the registry is already frozen;
//   - ErrAlreadyRegistered  if a provider with the same Name exists.
//
// The ErrEmptyName check runs before Lock to short-circuit malformed
// providers without acquiring the registry's mutex.
func (r *Registry) Register(p Provider) error {
	if p == nil {
		return ErrNilProvider
	}
	// Detect typed-nil interfaces: `var p Provider = someNilPtr`
	// produces a non-nil interface whose underlying pointer is nil;
	// calling a method on it would panic. The Kind==Ptr guard
	// short-circuits non-pointer kinds where IsNil would itself
	// panic.
	if rv := reflect.ValueOf(p); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return ErrNilProvider
	}
	name := p.Name()
	if name == "" {
		return ErrEmptyName
	}
	entry := &ProviderEntry{
		Name:     name,
		Provider: p,
	}
	// Migration step: map Provider.Capabilities() []Capability to the
	// typed ProviderCapabilitySet pointer fields. Each pointer is
	// left nil when the provider does NOT advertise the capability
	// (zero-default path makes "no enrichment declared" the safe
	// baseline).
	for _, c := range p.Capabilities() {
		switch c {
		case CapabilitySearch:
			if entry.Capabilities.Search == nil {
				entry.Capabilities.Search = &ProviderCapabilityDetail{}
			}
		case CapabilityFetch:
			if entry.Capabilities.Fetch == nil {
				entry.Capabilities.Fetch = &ProviderCapabilityDetail{}
			}
		case CapabilityVideo:
			if entry.Capabilities.Video == nil {
				entry.Capabilities.Video = &ProviderCapabilityDetail{}
			}
		case CapabilityImage:
			if entry.Capabilities.Image == nil {
				entry.Capabilities.Image = &ProviderCapabilityDetail{}
			}
		case CapabilityMusic:
			if entry.Capabilities.Music == nil {
				entry.Capabilities.Music = &ProviderCapabilityDetail{}
			}
		case CapabilityVoice:
			if entry.Capabilities.Voice == nil {
				entry.Capabilities.Voice = &ProviderCapabilityDetail{}
			}
		case CapabilityScript:
			if entry.Capabilities.Script == nil {
				entry.Capabilities.Script = &ProviderCapabilityDetail{}
			}
		}
	}
	return r.registerEntryLocked(entry)
}

// RegisterEntry adds a fully-formed ProviderEntry to the registry.
// S3a (June 2026): the canonical entry-point for callers that want
// to attach per-capability details, a HealthProbe, Timeout, or
// MaxResults/MaxPages limits. Register(p) is a thin wrapper that
// builds the entry's name + capabilities from a Provider; this
// method accepts the already-typed entry as-is.
//
// Returns:
//   - ErrNilEntry           if entry is nil;
//   - ErrEmptyName          if entry.Name is "" AND entry.Provider is nil
//     (forward-compat: callers may pass entry
//     with explicit Name but no Provider);
//   - ErrFrozen             if the registry is already frozen;
//   - ErrAlreadyRegistered  if a provider with the same Name exists.
func (r *Registry) RegisterEntry(entry *ProviderEntry) error {
	if entry == nil {
		return ErrNilEntry
	}
	if entry.Name == "" {
		if entry.Provider == nil {
			return ErrEmptyName
		}
		entry.Name = entry.Provider.Name()
		if entry.Name == "" {
			return ErrEmptyName
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrFrozen
	}
	if _, exists := r.entries[entry.Name]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, entry.Name)
	}
	r.entries[entry.Name] = entry
	return nil
}

// registerEntryLocked is the internal helper that takes the lock and
// inserts the entry. Called by Register (back-compat path) and
// RegisterEntry (canonical path) so the lock acquisition pattern is
// in one place. Caller must hold (or skip, via the wrapping Lock in
// RegisterEntry) the appropriate lock contract.
func (r *Registry) registerEntryLocked(entry *ProviderEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrFrozen
	}
	if _, exists := r.entries[entry.Name]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, entry.Name)
	}
	r.entries[entry.Name] = entry
	return nil
}

// Freeze locks the registry. Idempotent: safe to call multiple times.
// After Freeze, Register returns ErrFrozen and lookups become
// effectively wait-free.
func (r *Registry) Freeze() {
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

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
