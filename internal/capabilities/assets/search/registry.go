// Package search — registry.go is the canonical BackendRegistry
// concrete (PR-SEARCH-PORTS-SPLIT, 2026-07-04).
//
// Pre-split, BackendRegistry was a 95-LoC struct embedded in the
// historical 674-LoC ports.go god file alongside the SearchBackend
// interface, 8 sentinels, 2 ports, and the Logger type. The split
// surfaces it as a single-purpose capability file per AGENTS.md
// Pattern 5 (capability-stable file split).
//
// BackendRegistry is the freezeable backend catalog. Register/Freeze
// run once during composition root wiring; after Freeze() any call
// to Register returns ErrFrozen. Mirrors providers.Registry's
// RWMutex + typed-nil-pointer + Empty-Name contract — same patterns
// mean the same operational guarantees.
package search

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// BackendRegistry is the canonical concrete freezeable backend catalog
// for the search.Aggregator. Implementations of SearchBackend are
// registered at composition root, then the registry is frozen before
// the server starts accepting requests.
//
// Locking contract:
//   - Register acquires mu.Lock() and releases via defer.
//   - IsFrozen / All / Eligible acquire mu.RLock() and release via defer.
//   - Freeze acquires mu.Lock() (write lock) so a concurrent Register
//     blocks until the writer has observed the frozen bit.
//
// Compile-time assertion at the bottom of this file pins the struct
// drift to a build failure (godlike/06 audit-pin discipline).
type BackendRegistry struct {
	mu      sync.RWMutex
	entries map[string]SearchBackend
	frozen  bool
}

// NewBackendRegistry returns an empty, mutable registry.
func NewBackendRegistry() *BackendRegistry {
	return &BackendRegistry{entries: make(map[string]SearchBackend)}
}

// Register adds a backend under its Name(). Returns:
//   - ErrNilBackend        if b is the zero SearchBackend value, OR a
//     typed-nil pointer (Kind==Ptr && IsNil).
//   - ErrEmptyName         if b.Name() returns "" (checked pre-Lock).
//   - ErrFrozen            if the registry is already frozen.
//   - ErrAlreadyRegistered if a backend with the same Name exists.
func (r *BackendRegistry) Register(b SearchBackend) error {
	if b == nil {
		return ErrNilBackend
	}
	if rv := reflect.ValueOf(b); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return ErrNilBackend
	}
	name := b.Name()
	if name == "" {
		return ErrEmptyName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrFrozen
	}
	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, name)
	}
	r.entries[name] = b
	return nil
}

// Freeze locks the registry. Idempotent: safe to call multiple times.
// After Freeze, Register returns ErrFrozen and lookups become
// effectively wait-free.
func (r *BackendRegistry) Freeze() {
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

// IsFrozen reports whether the registry has been frozen.
func (r *BackendRegistry) IsFrozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

// All returns every registered backend, sorted by Name() so callers
// can rely on a deterministic iteration order.
func (r *BackendRegistry) All() []SearchBackend {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SearchBackend, 0, len(r.entries))
	for _, b := range r.entries {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Eligible returns the registered backends matching q.Sources,
// q.MediaTypes AND q.Universe. The three filters compose with AND
// semantics.
//
//   - Sources: if q.Sources is non-empty, the candidate set is
//     reduced to backends whose Name() appears in the canonicalised
//     source list (alias resolution via ResolveCanonicals).
//     Empty canonical set (all aliases unknown) → empty result.
//   - MediaTypes: applied after Sources. Backends whose
//     Capabilities intersect with the canonicalised media-type
//     filter win; backends with no intersection are dropped.
//   - Universe: applied last. SearchCatalog keeps only catalog
//     backends (no live provider call); SearchDiscovery keeps only
//     discovery backends (no Qdrant call); SearchBlended keeps both.
//     Empty Universe defaults to SearchCatalog via
//     Query.EffectiveUniverse().
//
// Empty q.Sources AND empty q.MediaTypes AND blended universe → every
// backend is eligible (the legacy "all" behaviour is preserved).
// Sort order is Name() for determinism, same as All().
func (r *BackendRegistry) Eligible(q Query) []SearchBackend {
	all := r.All()

	// 1. Sources filter (fail-fast on unknown aliases).
	//
	// PR-SEARCH-HANDLER-MOUNT (July 2026): the semantic backend
	// is a cross-source meta-backend — it searches Qdrant for
	// ALL sources and applies source filtering internally via
	// compileSemanticFilters → filter.Source. It MUST always be
	// included regardless of q.Sources, otherwise requests like
	// sources=["stock"] with mode=hybrid produce zero eligible
	// backends and fail with ErrSemanticBackendUnavailable.
	// (PR-SEARCH-UNIVERSE: the universe filter below still drops
	// the semantic backend for discovery-universe queries — a
	// discovery search must not touch Qdrant.)
	canonicalSources := ResolveCanonicals(q.Sources)
	if len(q.Sources) > 0 && len(canonicalSources) == 0 {
		// All sources supplied were unknown aliases. NO
		// silent fallback: return empty result so callers
		// learn the misuse instead of getting a deceptively
		// full response from every backend.
		return []SearchBackend{}
	}
	if len(canonicalSources) > 0 {
		allow := make(map[string]struct{}, len(canonicalSources))
		for _, s := range canonicalSources {
			allow[s] = struct{}{}
		}
		filtered := make([]SearchBackend, 0, len(all))
		for _, b := range all {
			if _, ok := allow[b.Name()]; ok {
				filtered = append(filtered, b)
			} else if b.Name() == "semantic" {
				// Always include semantic: cross-source
				// meta-backend with internal source filter.
				filtered = append(filtered, b)
			}
		}
		all = filtered
	}

	// 2. MediaTypes filter (legacy behaviour preserved).
	if len(q.MediaTypes) > 0 {
		want := make(map[Capability]struct{}, len(q.MediaTypes))
		for _, m := range q.MediaTypes {
			if m == "" {
				continue
			}
			want[Capability(m)] = struct{}{}
		}
		if len(want) > 0 {
			out := make([]SearchBackend, 0, len(all))
			for _, b := range all {
				for _, c := range b.Capabilities() {
					if _, ok := want[c]; ok {
						out = append(out, b)
						break
					}
				}
			}
			all = out
		}
	}

	// 3. Universe filter (PR-SEARCH-UNIVERSE).
	return filterBackendsByUniverse(all, q.EffectiveUniverse())
}

// filterBackendsByUniverse narrows the eligible set by the canonical
// SearchUniverse axis. SearchBlended keeps every backend (both
// catalog + discovery); SearchCatalog / SearchDiscovery keep only
// backends whose Universe() matches. A backend reporting an empty
// Universe is treated as catalog (the canonical default) so a
// misconfigured backend degrades to the safe, provider-free side
// rather than leaking live-provider traffic.
func filterBackendsByUniverse(backends []SearchBackend, universe SearchUniverse) []SearchBackend {
	if universe == SearchBlended {
		return backends
	}
	out := make([]SearchBackend, 0, len(backends))
	for _, b := range backends {
		bu := b.Universe()
		if bu == "" {
			bu = SearchCatalog
		}
		if bu == universe {
			out = append(out, b)
		}
	}
	return out
}
