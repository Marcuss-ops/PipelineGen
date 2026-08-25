package providerassets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// Sentinel errors for the registry.
var (
	ErrAlreadyRegistered = errors.New("providerassets: adapter already registered")
	ErrNotFound          = errors.New("providerassets: adapter not found")
	ErrNilAdapter        = errors.New("providerassets: nil adapter")
	ErrEmptyName         = errors.New("providerassets: adapter name is empty")
	ErrRegistryFrozen    = errors.New("providerassets: registry is frozen")
)

// Registry is a catalog of ProviderAdapter implementations. It is safe
// for concurrent use and becomes immutable after Freeze.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]ProviderAdapter
	frozen  bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]ProviderAdapter)}
}

// Register adds a ProviderAdapter to the catalog.
func (r *Registry) Register(adapter ProviderAdapter) error {
	if adapter == nil {
		return ErrNilAdapter
	}
	name := adapter.Name()
	if name == "" {
		return ErrEmptyName
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrRegistryFrozen
	}
	if _, exists := r.entries[name]; exists {
		return ErrAlreadyRegistered
	}
	r.entries[name] = adapter
	return nil
}

// Get returns the adapter with the given provider name.
func (r *Registry) Get(name string) (ProviderAdapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.entries[name]
	if !ok {
		return nil, ErrNotFound
	}
	return adapter, nil
}

// All returns all registered adapters sorted by name.
func (r *Registry) All() []ProviderAdapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProviderAdapter, 0, len(r.entries))
	for _, adapter := range r.entries {
		out = append(out, adapter)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}

// Names returns the sorted list of registered provider names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Freeze marks the registry as immutable. Subsequent Register calls
// return an error.
func (r *Registry) Freeze() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
}

// IsFrozen reports whether the registry is frozen.
func (r *Registry) IsFrozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

// Search queries a single provider by name.
func (r *Registry) Search(ctx context.Context, provider string, req SearchRequest) (SearchResult, error) {
	adapter, err := r.Get(provider)
	if err != nil {
		return SearchResult{}, err
	}
	return adapter.Search(ctx, req)
}

// SearchAll queries every registered provider in parallel and returns
// a merged result. Errors from individual providers are collected but
// do not abort the fan-out.
func (r *Registry) SearchAll(ctx context.Context, req SearchRequest) (SearchResult, []error) {
	adapters := r.All()
	if len(adapters) == 0 {
		return SearchResult{}, nil
	}

	var wg sync.WaitGroup
	results := make([]SearchResult, len(adapters))
	errs := make([]error, len(adapters))
	var mu sync.Mutex

	for i, adapter := range adapters {
		wg.Add(1)
		concurrent.SafeGoFunc("provider-search", struct {
			idx int
			a   ProviderAdapter
		}{i, adapter}, func(in struct {
			idx int
			a   ProviderAdapter
		}) {
			idx, a := in.idx, in.a
			defer wg.Done()
			res, err := a.Search(ctx, req)
			mu.Lock()
			results[idx] = res
			errs[idx] = err
			mu.Unlock()
		})
	}

	wg.Wait()

	var merged []ProviderAsset
	var combinedErrs []error
	for i, res := range results {
		if errs[i] != nil {
			combinedErrs = append(combinedErrs, fmt.Errorf("providerassets: %s: %w", adapters[i].Name(), errs[i]))
			continue
		}
		merged = append(merged, res.Assets...)
	}

	return SearchResult{Assets: merged}, combinedErrs
}
