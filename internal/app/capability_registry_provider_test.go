package app

import (
	"context"
	"errors"
	"testing"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
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

// TestRegisterCapabilities_RegistersPreparedProvidersBeforeFreeze pins the
// composition lifecycle: adapters are prepared first, registered in the
// canonical phase, and only then is the provider registry frozen. This keeps
// the search graph and the final provider catalog on the same adapter set.
func TestRegisterCapabilities_RegistersPreparedProvidersBeforeFreeze(t *testing.T) {
	providerRegistry := providers.NewRegistry()
	apiRegistry := module.NewRegistry()
	provider := &lifecycleSearchProvider{name: "late-search"}
	prepared := PreparedCapabilities{Providers: []TrackedProviderEntry{{
		Id:     provider.Name(),
		Kind:   ProviderKindSearch,
		Search: provider,
	}}}

	err := registerCapabilities(apiRegistry, providerRegistry, CapabilityDeps{
		Providers: prepared,
	})
	if err != nil {
		t.Fatalf("registerCapabilities: %v", err)
	}
	if !providerRegistry.IsFrozen() {
		t.Fatal("provider registry must be frozen after canonical registration")
	}
	got, ok := providerRegistry.Get(provider.Name())
	if !ok || got != provider {
		t.Fatalf("registered provider = (%v, %t), want exact prepared adapter (%v, true)", got, ok, provider)
	}

	late := &lifecycleSearchProvider{name: "registered-too-late"}
	if err := providerRegistry.RegisterSearch(late); !errors.Is(err, providers.ErrFrozen) {
		t.Fatalf("late provider registration error = %v, want providers.ErrFrozen", err)
	}
}
