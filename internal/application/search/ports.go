package search

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// SearchBackend is the contract every aggregator backend satisfies.
//
// A SearchBackend alone is not responsible for cross-tenant filtering,
// indexing, or asset lifecycle transitions — it just runs a query
// against one source and returns up to req.Limit candidates that survive
// the backend's own scoring. The aggregator owns dedup, ranking, and
// cursor stability across the whole backend fan-out.
//
// Implementations live under internal/app (composition root owns the
// ONLY capability-crossing adapters per Wave 19):
//   - providerBackendAdapter   wraps providers.SearchProvider (SSOT registry)
//   - localBackendAdapter      wraps assets.ClipsRepository (kernel search)
//   - semanticBackendAdapter   wraps mediasearch.Service (cross-cap port bridge)
type SearchBackend interface {
	// Name returns the human-readable backend identifier
	// (e.g. "youtube","artlist","local","semantic"). Must be unique
	// within a BackendRegistry. Stable across calls. Empty Name
	// triggers the registry's typed-nil-and-empty guards.
	Name() string

	// Capabilities advertises which MediaTypes this backend returns.
	// Used by BackendRegistry.Eligible to filter by Query.MediaTypes.
	// Eg. a video-only provider returns []Capability{CapVideo}.
	Capabilities() []Capability

	// Search runs the query and returns up to req.Limit candidates
	// matching q. The aggregator respects ctx.Done(): the backend
	// MUST honour cancellation and avoid leaking goroutines after
	// the call ends. Errors propagate to Result.ProviderErrors[name]
	// with Result.Partial = true (the aggregator NEVER fails the
	// whole search on a single backend error; partial is preferred).
	Search(ctx context.Context, q Query) ([]Candidate, error)
}

// ── BackendRegistry ────────────────────────────────────────────────
//
// BackendRegistry is the freezeable backend catalog. Register/Freeze
// run once during composition root wiring; after Freeze() any call
// to Register returns ErrFrozen. Mirrors providers.Registry's
// RWMutex + typed-nil-pointer + Empty-Name contract — same patterns
// mean the same operational guarantees.
type BackendRegistry struct {
	mu      sync.RWMutex
	entries map[string]SearchBackend
	frozen  bool
}

// Sentinel errors. Mirrors providers.Registry so operator muscle
// memory transfers between the two.
var (
	ErrAlreadyRegistered = errors.New("search: backend already registered")
	ErrFrozen            = errors.New("search: registry frozen")
	ErrNilBackend        = errors.New("search: nil backend")
	ErrEmptyName         = errors.New("search: backend Name() returned empty")
)

// NewBackendRegistry returns an empty, mutable registry.
func NewBackendRegistry() *BackendRegistry {
	return &BackendRegistry{entries: make(map[string]SearchBackend)}
}

// Register adds a backend under its Name(). Returns:
//   - ErrNilBackend        if b is the zero SearchBackend value, OR a
//                          typed-nil pointer (Kind==Ptr && IsNil).
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

// Eligible returns the registered backends whose Capabilities
// intersect with q.MediaTypes. If q.MediaTypes is empty, every
// backend is eligible (caller asked for "all media types").
// Sort order is Name() for determinism, same as All().
func (r *BackendRegistry) Eligible(q Query) []SearchBackend {
	all := r.All()
	if len(q.MediaTypes) == 0 {
		return all
	}
	want := make(map[Capability]struct{}, len(q.MediaTypes))
	for _, m := range q.MediaTypes {
		if m == "" {
			continue
		}
		want[Capability(m)] = struct{}{}
	}
	if len(want) == 0 {
		return all
	}
	out := make([]SearchBackend, 0, len(all))
	for _, b := range all {
		for _, c := range b.Capabilities() {
			if _, ok := want[c]; ok {
				out = append(out, b)
				break
			}
		}
	}
	return out
}

// ── Logger port ─────────────────────────────────────────────────────
//
// Logger is the narrow logging surface used by Aggregator + adapters.
// Type compatibility: search.Logger has the same shape as the
// existing assets/search.Logger; production adapters usually
// implement it via zapLogAdapter (see internal/app/assets_adapters.go).
type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
}

// noopLogger swallows every log call; used when callers pass nil
// and in tests where noise must be zero. Mirrors other noop loggers
// in internal/application/*.
type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Error(string, ...any) {}
