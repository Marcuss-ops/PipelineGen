package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	appdiag "github.com/Marcuss-ops/PipelineGen/internal/application/assets/diagnostics"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// ── Diagnostics adapters ───────────────────────────────────────────────

// diagIndexHealthAdapter adapts *realtime.Service to diagnostics.IndexHealthPort.
type diagIndexHealthAdapter struct {
	realtime  interface{} // was *realtime.Service (package removed)
	vectorSvc *qdrant.Service
}

func (a *diagIndexHealthAdapter) IndexHealth(ctx context.Context) (*appdiag.IndexHealthReport, error) {
	return nil, fmt.Errorf("realtime service unavailable — package removed")
}

func (a *diagIndexHealthAdapter) VectorStore() appdiag.VectorStorePort {
	if a.vectorSvc == nil {
		return nil
	}
	return &diagVectorStoreAdapter{svc: a.vectorSvc}
}

// diagVectorStoreAdapter adapts *qdrant.Service to diagnostics.VectorStorePort.
type diagVectorStoreAdapter struct {
	svc *qdrant.Service
}

func (a *diagVectorStoreAdapter) Health(ctx context.Context) error {
	return a.svc.Health(ctx)
}

func (a *diagVectorStoreAdapter) OperationCollectionInfo(ctx context.Context) (*appdiag.CollectionInfo, error) {
	info, err := a.svc.OperationCollectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, fmt.Errorf("OperationCollectionInfo returned nil")
	}
	// AGENT-1 cascade fix (June 2026): the canonical qdrant stub
	// (internal/infrastructure/qdrant/service.go::CollectionInfo) declares
	// PointsCount as `int` while diagnostics.CollectionInfo (the app
	// port) declares it as `int64`. Cast at the adapter boundary so
	// callers receive the canonical wider type.
	return &appdiag.CollectionInfo{PointsCount: int64(info.PointsCount)}, nil
}

// diagAssetStatsAdapter adapts *assets.ClipsRepository to diagnostics.AssetStatsPort.
type diagAssetStatsAdapter struct {
	clips *assetsrepo.ClipsRepository
}

func (a *diagAssetStatsAdapter) GetStats(ctx context.Context) (*appdiag.AssetStats, error) {
	total, err := a.clips.CountAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("CountAll: %w", err)
	}
	indexed, err := a.clips.CountIndexed(ctx)
	if err != nil {
		return nil, fmt.Errorf("CountIndexed: %w", err)
	}
	return &appdiag.AssetStats{
		Total: int(total),
		ByType: map[string]int{
			"total":   int(total),
			"indexed": int(indexed),
		},
		ByStatus: map[string]int{
			"ready": int(total),
		},
	}, nil
}

// ── Search adapters ────────────────────────────────────────────────────

// searchRegistryAdapter adapts *providers.Registry to search.SearchProviderRegistry.
type searchRegistryAdapter struct {
	registry *providers.Registry
}

func (a *searchRegistryAdapter) SearchProviders() []appsearch.SearchProviderPort {
	if a.registry == nil {
		return nil
	}
	all := a.registry.All()
	out := make([]appsearch.SearchProviderPort, 0, len(all))
	for _, p := range all {
		sp, ok := p.(providers.SearchProvider)
		if !ok {
			continue
		}
		out = append(out, &searchProviderAdapter{provider: sp})
	}
	return out
}

// searchProviderAdapter adapts a single providers.SearchProvider to search.SearchProviderPort.
type searchProviderAdapter struct {
	provider providers.SearchProvider
}

func (a *searchProviderAdapter) Name() string {
	return a.provider.Name()
}

func (a *searchProviderAdapter) Capabilities() []string {
	caps := a.provider.Capabilities()
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = string(c)
	}
	return out
}

func (a *searchProviderAdapter) Search(ctx context.Context, req appsearch.SearchRequest) (*appsearch.SearchResult, error) {
	provReq := providers.SearchRequest{
		Query: req.Query,
		Limit: req.Limit,
	}
	if req.MediaType != "" && req.MediaType != "all" {
		provReq.Filters.MediaTypes = []asset.MediaType{asset.MediaType(req.MediaType)}
	}
	result, err := a.provider.Search(ctx, provReq)
	if err != nil {
		return nil, err
	}
	candidates := make([]appsearch.SearchCandidate, len(result.Candidates))
	for i, c := range result.Candidates {
		dur := c.Duration.Seconds()
		candidates[i] = appsearch.SearchCandidate{
			SourceRef:    c.SourceRef,
			Title:        c.Title,
			ThumbnailURL: c.ThumbnailURL,
			PreviewURL:   c.PreviewURL,
			Duration:     dur,
			Score:        c.Score,
		}
	}
	return &appsearch.SearchResult{
		Candidates:    candidates,
		NextPageToken: result.NextPageToken,
	}, nil
}

// searchVectorAdapter combines the embedder and vector store into search.VectorSearchPort.
type searchVectorAdapter struct {
	embedder   appsearch.VectorStorePort
	realtimeSvc interface{} // was *realtime.Service (package removed)
}

func (a *searchVectorAdapter) EmbedTextForVector(ctx context.Context, text, vectorName string) ([]float32, error) {
	return nil, fmt.Errorf("realtime service not configured — package removed")
}

func (a *searchVectorAdapter) VectorStore() appsearch.VectorStorePort {
	return a.embedder
}

// searchCatalogAdapter adapts *catalog.Repository to search.LocalCatalogPort.
type searchCatalogAdapter struct {
	catalog *catalog.Repository
}

func (a *searchCatalogAdapter) SearchAll(ctx context.Context, query string) ([]appsearch.CatalogSearchResult, error) {
	records, err := a.catalog.SearchAll(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]appsearch.CatalogSearchResult, len(records))
	for i, r := range records {
		out[i] = appsearch.CatalogSearchResult{
			ID:    r.ID,
			Name:  r.Name,
			Type:  r.MediaType,
			Score: 0,
		}
	}
	return out, nil
}

// searchClipAdapter adapts *catalog.Repository to search.LocalClipPort.
type searchClipAdapter struct {
	catalog *catalog.Repository
}

func (a *searchClipAdapter) SearchClips(ctx context.Context, source, query string) ([]appsearch.LocalClipResult, error) {
	records, err := a.catalog.SearchClips(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]appsearch.LocalClipResult, 0, len(records))
	for _, r := range records {
		if source != "" && source != "all" && r.Source != source {
			continue
		}
		out = append(out, appsearch.LocalClipResult{
			ID:   r.ID,
			Name: r.Name,
		})
	}
	return out, nil
}

// searchConfigAdapter adapts *config.Config to search.ConfigPort.
type searchConfigAdapter struct {
	cfg *config.Config
}

func (a *searchConfigAdapter) VectorConfig() appsearch.VectorConfig {
	return appsearch.VectorConfig{
		TextVectorName:       a.cfg.VectorSearch.TextVectorName,
		VisualVectorName:     a.cfg.VectorSearch.VisualVectorName,
		AudioVectorName:      a.cfg.VectorSearch.AudioVectorName,
		TranscriptVectorName: a.cfg.VectorSearch.TranscriptVectorName,
		MinInstantScore:      a.cfg.VectorSearch.MinInstantScore,
	}
}

// zapDiagLogAdapter adapts *zap.Logger to diagnostics.Logger.
type zapDiagLogAdapter struct {
	log *zap.Logger
}

func (a *zapDiagLogAdapter) Info(msg string, keysAndValues ...any) {
	a.log.Sugar().Infow(msg, keysAndValues...)
}
func (a *zapDiagLogAdapter) Warn(msg string, keysAndValues ...any) {
	a.log.Sugar().Warnw(msg, keysAndValues...)
}
func (a *zapDiagLogAdapter) Error(msg string, keysAndValues ...any) {
	a.log.Sugar().Errorw(msg, keysAndValues...)
}

// zapSearchLogAdapter adapts *zap.Logger to search.Logger.
type zapSearchLogAdapter struct {
	log *zap.Logger
}

func (a *zapSearchLogAdapter) Info(msg string, keysAndValues ...any) {
	a.log.Sugar().Infow(msg, keysAndValues...)
}
func (a *zapSearchLogAdapter) Warn(msg string, keysAndValues ...any) {
	a.log.Sugar().Warnw(msg, keysAndValues...)
}
func (a *zapSearchLogAdapter) Error(msg string, keysAndValues ...any) {
	a.log.Sugar().Errorw(msg, keysAndValues...)
}
func (a *zapSearchLogAdapter) Debug(msg string, keysAndValues ...any) {
	a.log.Sugar().Debugw(msg, keysAndValues...)
}
