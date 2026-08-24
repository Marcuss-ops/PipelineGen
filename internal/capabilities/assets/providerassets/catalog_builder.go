package providerassets

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrNilProviderPolicy = errors.New("providerassets: nil provider policy")
	ErrDuplicatePolicy   = errors.New("providerassets: duplicate provider policy")
	ErrProviderDisabled  = errors.New("providerassets: provider disabled")
)

// ProviderPolicy is the composition-time policy for one external catalog
// provider. Provider selection and enablement are centralized here; adapters
// only implement transport and canonical result mapping.
type ProviderPolicy struct {
	Name      string
	Enabled   bool
	MediaType string
	Priority  int
}

// ProviderPolicyRegistry is an immutable provider-policy index.
type ProviderPolicyRegistry struct {
	byName map[string]ProviderPolicy
}

// NewProviderPolicyRegistry builds a deterministic policy registry.
func NewProviderPolicyRegistry(policies []ProviderPolicy) (*ProviderPolicyRegistry, error) {
	byName := make(map[string]ProviderPolicy, len(policies))
	for _, policy := range policies {
		if policy.Name == "" {
			return nil, ErrNilProviderPolicy
		}
		if _, exists := byName[policy.Name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicatePolicy, policy.Name)
		}
		byName[policy.Name] = policy
	}
	return &ProviderPolicyRegistry{byName: byName}, nil
}

// Get returns a provider policy by name.
func (r *ProviderPolicyRegistry) Get(name string) (ProviderPolicy, bool) {
	if r == nil {
		return ProviderPolicy{}, false
	}
	policy, ok := r.byName[name]
	return policy, ok
}

// Enabled returns whether the provider is enabled by policy.
func (r *ProviderPolicyRegistry) Enabled(name string) bool {
	policy, ok := r.Get(name)
	return ok && policy.Enabled
}

// Names returns policy names in priority order, then lexicographically.
func (r *ProviderPolicyRegistry) Names() []string {
	if r == nil {
		return nil
	}
	policies := make([]ProviderPolicy, 0, len(r.byName))
	for _, policy := range r.byName {
		policies = append(policies, policy)
	}
	sort.Slice(policies, func(i, j int) bool {
		if policies[i].Priority != policies[j].Priority {
			return policies[i].Priority < policies[j].Priority
		}
		return policies[i].Name < policies[j].Name
	})
	names := make([]string, len(policies))
	for i, policy := range policies {
		names[i] = policy.Name
	}
	return names
}

// CatalogBuilder is the sole composition-time builder for the external
// provider catalog. It applies ProviderPolicyRegistry before freezing the
// adapter registry, so disabled providers cannot be exposed accidentally.
type CatalogBuilder struct {
	policies *ProviderPolicyRegistry
	adapters []ProviderAdapter
}

// NewCatalogBuilder creates a builder backed by the supplied provider policy.
func NewCatalogBuilder(policies *ProviderPolicyRegistry) *CatalogBuilder {
	return &CatalogBuilder{policies: policies}
}

// Add queues an adapter for registration. Registration happens in Build so
// policy filtering and duplicate detection are performed in one place.
func (b *CatalogBuilder) Add(adapter ProviderAdapter) error {
	if b == nil || adapter == nil {
		return ErrNilAdapter
	}
	b.adapters = append(b.adapters, adapter)
	return nil
}

// Build applies policy, registers adapters once, and freezes the catalog.
func (b *CatalogBuilder) Build() (*Registry, error) {
	if b == nil {
		return nil, ErrNilAdapter
	}
	if b.policies == nil {
		return nil, ErrNilProviderPolicy
	}
	registry := NewRegistry()
	for _, adapter := range b.adapters {
		policy, ok := b.policies.Get(adapter.Name())
		if !ok || !policy.Enabled {
			return nil, fmt.Errorf("%w: %q", ErrProviderDisabled, adapter.Name())
		}
		if err := registry.Register(adapter); err != nil {
			return nil, err
		}
	}
	registry.Freeze()
	return registry, nil
}
