package retrieved

import (
	"context"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
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

func (p *WikimediaCommonsProvider) Name() detail.ImageProvider {
	return detail.ProviderWikimediaCommons
}

func (p *WikimediaCommonsProvider) Healthy(_ context.Context) error { return nil }

func (p *WikimediaCommonsProvider) Search(ctx context.Context, query string, _ RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	if p == nil || p.bridge == nil {
		return nil, nil
	}
	hit := p.bridge.SearchWikimediaCommons(ctx, query)
	if hit.PreviewURL == "" || hit.License == "" {
		return nil, nil
	}
	hit.Provider = detail.ProviderWikimediaCommons
	hit.Origin = detail.ImageOriginRetrieved
	return []RetrievalSearchResult{hit}, nil
}

func (p *WikimediaCommonsProvider) ID() string { return string(p.Name()) }
