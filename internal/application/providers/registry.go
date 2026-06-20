package providers

import (
	"errors"
	"fmt"
	"sync"
)

// Sentinel errors for the registry.
var (
	// ErrAlreadyRegistered is returned when a provider with the same
	// Name is already present. The returned error is wrapped with
	// %w so errors.Is(err, ErrAlreadyRegistered) matches.
	ErrAlreadyRegistered = errors.New("providers: provider already registered")

	// ErrFrozen is returned when Register is called after Freeze.
	ErrFrozen = errors.New("providers: registry frozen")

	// ErrNilProvider is returned when Register is called with a nil
	// Provider interface value.
	ErrNilProvider = errors.New("providers: nil provider")
)

// Registry is a one-shot, freezeable provider catalog.
// Register/Freeze are expected to run once during composition root
// wiring; after Freeze() any call to Register() returns ErrFrozen.
//
// Concurrency: writes use sync.RWMutex.Lock; lookups (Get, All,
// ByCapability, IsFrozen) use RLock and are effectively wait-free
// after Freeze. Freeze is naturally idempotent: concurrent calls
// converge on the same final state with no data dependency.
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
//   - ErrNilProvider        if p is nil;
//   - ErrFrozen             if the registry is already frozen;
//   - ErrAlreadyRegistered  if a provider with the same Name exists.
func (r *Registry) Register(p Provider) error {
	if p == nil {
		return ErrNilProvider
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrFrozen
	}
	name := p.Name()
	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, name)
	}
	r.entries[name] = p
	return nil
}

// Freeze locks the registry. Idempotent: safe to call multiple
// times. After Freeze, Register returns ErrFrozen and lookups
// become effectively wait-free.
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
// given capability. Order is not guaranteed.
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
	return out
}

// All returns a snapshot of every registered provider. The returned
// slice and its elements must not be mutated by the caller.
func (r *Registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.entries))
	for _, p := range r.entries {
		out = append(out, p)
	}
	return out
}

// IsFrozen reports whether the registry has been frozen.
func (r *Registry) IsFrozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}
