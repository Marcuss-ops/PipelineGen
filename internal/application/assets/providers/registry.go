package providers

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
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
)

// Registry is a one-shot, freezeable provider catalog.
// Register/Freeze run once during composition root wiring; after
// Freeze() any call to Register() returns ErrFrozen.
//
// Concurrency: writes use sync.RWMutex.Lock; lookups (Get, All,
// ByCapability, IsFrozen) use RLock and are effectively wait-free
// after Freeze. Freeze is naturally idempotent — concurrent calls
// converge on the same final state with no data dependency.
//
// Determinism: All() and ByCapability() return slices sorted by
// Name() so callers can rely on a stable iteration order.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]Provider
	frozen  bool
}

// NewRegistry returns an empty, mutable registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]Provider)}
}

// Register adds a provider under its Name(). Returns:
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrFrozen
	}
	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, name)
	}
	r.entries[name] = p
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
	p, ok := r.entries[name]
	return p, ok
}

// ByCapability returns every registered provider advertising the
// given capability, sorted by Name() for deterministic iteration.
func (r *Registry) ByCapability(cap Capability) []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.entries))
	for _, p := range r.entries {
		for _, c := range p.Capabilities() {
			if c == cap {
				out = append(out, p)
				break
			}
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
	for _, p := range r.entries {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// IsFrozen reports whether the registry has been frozen.
func (r *Registry) IsFrozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
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
