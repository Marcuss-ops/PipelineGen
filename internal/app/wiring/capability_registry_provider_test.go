package wiring

import (
	"context"
	"errors"
	"testing"

	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
)

type lifecycleSearchProvider struct {
	name string
}

func (p *lifecycleSearchProvider) Name() string { return p.name }

func (p *lifecycleSearchProvider) Capabilities() []providers.Capability {
	return []providers.Capability{providers.CapabilitySearch, providers.CapabilityImage}
}

func (p *lifecycleSearchProvider) Search(context.Context, providers.SearchRequest) (providers.SearchResult, error) {
	return providers.SearchResult{}, nil
}

var _ providers.SearchProvider = (*lifecycleSearchProvider)(nil)

// TestBootstrapProviderRegistry_FreezesBeforeFinalPublication pins the
// composition lifecycle: adapters are registered first, the provider catalog
// is frozen, and final publication cannot mutate it.
func TestBootstrapProviderRegistry_FreezesBeforeFinalPublication(t *testing.T) {
	providerRegistry := providers.NewRegistry()
	apiRegistry := module.NewRegistry()
	provider := &lifecycleSearchProvider{name: "bootstrap-search"}
	entries := []TrackedProviderEntry{{Id: provider.Name(), Kind: ProviderKindSearch, Search: provider}}
	if err := bootstrapProviderRegistry(providerRegistry, entries, nil); err != nil {
		t.Fatalf("bootstrapProviderRegistry: %v", err)
	}
	if !providerRegistry.IsFrozen() {
		t.Fatal("provider registry must be frozen before search composition")
	}
	got, ok := providerRegistry.Get(provider.Name())
	if !ok || got != provider {
		t.Fatalf("registered provider = (%v, %t), want exact prepared adapter (%v, true)", got, ok, provider)
	}
	if err := registerCapabilities(apiRegistry, providerRegistry, CapabilityDeps{}); err != nil {
		t.Fatalf("registerCapabilities: %v", err)
	}
	late := &lifecycleSearchProvider{name: "registered-too-late"}
	if err := providerRegistry.RegisterSearch(late); !errors.Is(err, providers.ErrFrozen) {
		t.Fatalf("late provider registration error = %v, want providers.ErrFrozen", err)
	}
}

func TestBootstrapProviderRegistryRejectsAlreadyFrozenRegistry(t *testing.T) {
	providerRegistry := providers.NewRegistry()
	providerRegistry.Freeze()
	provider := &lifecycleSearchProvider{name: "late-bootstrap"}
	if err := bootstrapProviderRegistry(providerRegistry, []TrackedProviderEntry{{Id: provider.Name(), Kind: ProviderKindSearch, Search: provider}}, nil); !errors.Is(err, providers.ErrFrozen) {
		t.Fatalf("bootstrap on frozen registry error = %v, want providers.ErrFrozen", err)
	}
}
