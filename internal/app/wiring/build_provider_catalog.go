package wiring

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providerassets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providerassets/adapters"
	artlist "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
)

// providerCatalogPolicies is the canonical external-provider policy table.
//
// MEDIA TYPE ACCURACY (2026-09-19). ProviderPolicy.MediaType declares which
// surface the adapter serves. It must describe the endpoint the adapter
// ACTUALLY calls, not the vendor's broader offering:
//
//   - artlist -> the Artlist scraper (video)
//   - pexels  -> artlist/fallback.Pexels  -> Pexels /v1/videos/search (video)
//   - pixabay -> artlist/fallback.Pixabay -> Pixabay videos endpoint (video)
//
// All three adapters return clip-typed ProviderAssets
// (artlist/fallback sets asset.MediaTypeClip), so every entry says "video".
// The previous "image" declaration for pexels/pixabay was a leftover from the
// retired native image client (internal/platform/images/pexels, deleted
// 2026-09-19) and misdescribed the wired surface. Pinning it here keeps the
// next reader from re-introducing an image declaration over a video adapter.
func providerCatalogPolicies() []providerassets.ProviderPolicy {
	return []providerassets.ProviderPolicy{
		{Name: "artlist", Enabled: true, MediaType: "video", Priority: 10},
		{Name: "pexels", Enabled: true, MediaType: "video", Priority: 20},
		{Name: "pixabay", Enabled: true, MediaType: "video", Priority: 30},
	}
}

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
	policies, err := providerassets.NewProviderPolicyRegistry(providerCatalogPolicies())
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
