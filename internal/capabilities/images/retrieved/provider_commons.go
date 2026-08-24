package images

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// WikimediaCommonsProvider is the explicit-license retrieval source used
// before generic image search. It keeps the remote search provider separate
// from the VidRush provider taxonomy while preserving the source license in
// the shared retrieval DTO.
type WikimediaCommonsProvider struct {
	bridge StorageBridge
	log    *zap.Logger
}

func NewWikimediaCommonsProvider(bridge StorageBridge, log *zap.Logger) *WikimediaCommonsProvider {
	return &WikimediaCommonsProvider{bridge: bridge, log: log}
}

func (p *WikimediaCommonsProvider) Name() asset.ImageProvider {
	return asset.ProviderWikimediaCommons
}

func (p *WikimediaCommonsProvider) Healthy(_ context.Context) error { return nil }

func (p *WikimediaCommonsProvider) Search(ctx context.Context, query string, _ routing.RetrievalSearchOptions) ([]routing.RetrievalSearchResult, error) {
	if p == nil || p.bridge == nil {
		return nil, nil
	}
	hit := p.bridge.SearchWikimediaCommons(ctx, query)
	if hit.PreviewURL == "" || hit.License == "" {
		return nil, nil
	}
	hit.Provider = asset.ProviderWikimediaCommons
	hit.Origin = asset.ImageOriginRetrieved
	return []routing.RetrievalSearchResult{hit}, nil
}

func (p *WikimediaCommonsProvider) ID() string { return string(p.Name()) }
