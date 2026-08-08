package app

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets/adapters"
	artlist "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
)

// buildProviderAssetCatalog is the sole composition-root owner of the
// external provider catalog. Provider policy is applied before the registry
// is frozen, preventing a provider from becoming available through an
// unregistered or disabled path.
func buildProviderAssetCatalog(service *artlist.Service, pexels, pixabay artlist.Searcher) (*providerassets.Registry, error) {
	if service == nil {
		return nil, fmt.Errorf("provider asset catalog: artlist service is required")
	}
	if pexels == nil {
		return nil, fmt.Errorf("provider asset catalog: pexels searcher is required")
	}
	if pixabay == nil {
		return nil, fmt.Errorf("provider asset catalog: pixabay searcher is required")
	}
	policies, err := providerassets.NewProviderPolicyRegistry([]providerassets.ProviderPolicy{
		{Name: "artlist", Enabled: true, MediaType: "video", Priority: 10},
		{Name: "pexels", Enabled: true, MediaType: "image", Priority: 20},
		{Name: "pixabay", Enabled: true, MediaType: "image", Priority: 30},
	})
	if err != nil {
		return nil, fmt.Errorf("provider policy registry: %w", err)
	}

	builder := providerassets.NewCatalogBuilder(policies)
	if err := builder.Add(adapters.NewSearchProviderAdapter("artlist", artlist.NewLiveAdapter(service))); err != nil {
		return nil, fmt.Errorf("register artlist provider adapter: %w", err)
	}
	if err := builder.Add(adapters.NewSearcherAdapter("pexels", pexels)); err != nil {
		return nil, fmt.Errorf("register pexels provider adapter: %w", err)
	}
	if err := builder.Add(adapters.NewSearcherAdapter("pixabay", pixabay)); err != nil {
		return nil, fmt.Errorf("register pixabay provider adapter: %w", err)
	}
	registry, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("build provider catalog: %w", err)
	}
	return registry, nil
}
