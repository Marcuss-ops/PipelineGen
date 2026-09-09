// Package wiring contains composition-root adapters for the ScriptFlow media
// read surfaces. PostgreSQL + pgvector is the only media catalog authority;
// no Qdrant or SQLite media mirror participates in these adapters.
package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/script"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

// postgresSemanticSearchPort bridges the script SourceSearch contract to the
// canonical pgvector MediaSearcher. The searcher already hydrates metadata
// from media_assets in the same PostgreSQL SSOT, so there is no second
// Qdrant→SQLite validation/hydration leg.
type postgresSemanticSearchPort struct {
	searcher appsearch.VectorStorePort
	embedder coreasset.Embedder
	log      *zap.Logger
}

func (p *postgresSemanticSearchPort) SearchByText(ctx context.Context, query string, limit int, _ string) ([]usecase.SemanticSearchResult, error) {
	if p == nil || p.searcher == nil || p.embedder == nil {
		return nil, fmt.Errorf("postgres semantic search: media search dependencies are unavailable")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []usecase.SemanticSearchResult{}, nil
	}
	if limit <= 0 {
		limit = 10
	}

	embedding, err := p.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres semantic search: embed query: %w", err)
	}
	if len(embedding.Vector) == 0 {
		return nil, fmt.Errorf("postgres semantic search: embed query returned an empty vector")
	}
	searchLimit := limit * 3
	if searchLimit < 30 {
		searchLimit = 30
	}
	results, err := p.searcher.HybridSearch(ctx, appsearch.HybridSearchRequest{
		DenseVector:     embedding.Vector,
		DenseVectorName: appsearch.ChannelText,
		SparseText:      query,
		Limit:           searchLimit,
		MinScore:        0,
		LifecycleState:  append([]string(nil), appsearch.SearchableLifecycleStates...),
		IsSystem:        true,
	})
	if err != nil {
		return nil, fmt.Errorf("postgres semantic search: %w", err)
	}

	out := make([]usecase.SemanticSearchResult, 0, len(results))
	for _, result := range results {
		id := strings.TrimSpace(result.AssetID)
		mediaType := strings.ToLower(strings.TrimSpace(result.MediaType))
		if id == "" || (mediaType != "video" && mediaType != "clip") {
			continue
		}
		out = append(out, usecase.SemanticSearchResult{
			ClipID:              id,
			Name:                result.Name,
			Score:               result.Score,
			Transcript:          result.SearchText,
			VisualSummary:       result.SearchText,
			MediaType:           result.MediaType,
			AvailableByIngest:   true,
			AnchorCoverageRatio: 1.0,
		})
	}
	if p.log != nil {
		p.log.Info("postgres semantic search",
			zap.Int("postgres_results", len(results)),
			zap.Int("accepted_clips", len(out)),
		)
	}
	return out, nil
}

// postgresAssetSearchPort implements the canonical scripts AssetSearchPort on
// the same pgvector MediaSearcher used by SourceSearch. It intentionally
// rejects Qdrant-only filter shapes instead of silently ignoring them.
type postgresAssetSearchPort struct {
	searcher appsearch.VectorStorePort
	embedder coreasset.Embedder
}

func (p *postgresAssetSearchPort) SearchAssets(ctx context.Context, q scriptports.AssetSearchQuery) ([]scriptports.AssetSearchHit, error) {
	if p == nil || p.searcher == nil || p.embedder == nil {
		return nil, fmt.Errorf("postgres asset search: media search dependencies are unavailable")
	}
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return []scriptports.AssetSearchHit{}, nil
	}
	if strings.TrimSpace(q.FolderNormalizedGroup) != "" || len(q.ExcludeRightsStatuses) > 0 || len(q.ExcludeReviewStatuses) > 0 {
		return nil, fmt.Errorf("postgres asset search: legacy Qdrant-only folder/rights filters are not supported on the canonical media port")
	}

	source := strings.TrimSpace(q.Source)
	limit := q.Limit
	if limit <= 0 {
		limit = 20
		if strings.EqualFold(source, "stock") {
			limit = 5
		}
	}
	minScore := q.MinScore
	if minScore <= 0 {
		minScore = 0.5
		if strings.EqualFold(source, "stock") {
			minScore = 0.3
		}
	}
	mediaType := strings.TrimSpace(q.MediaType)
	if mediaType == "" {
		mediaType = "video"
	}
	lifecycleStates := append([]string(nil), appsearch.SearchableLifecycleStates...)
	if q.RequireActiveLifecycle {
		lifecycleStates = []string{"ACTIVE"}
	}

	embedding, err := p.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres asset search: embed query: %w", err)
	}
	if len(embedding.Vector) == 0 {
		return nil, fmt.Errorf("postgres asset search: embed query returned an empty vector")
	}
	results, err := p.searcher.Search(ctx, appsearch.VectorSearchRequest{
		QueryVector:    embedding.Vector,
		VectorName:     appsearch.ChannelText,
		Limit:          limit,
		MinScore:       minScore,
		Source:         source,
		Category:       strings.TrimSpace(q.Category),
		MediaType:      mediaType,
		LifecycleState: lifecycleStates,
		WorkspaceID:    strings.TrimSpace(q.WorkspaceID),
		IsSystem:       q.IsSystem,
	})
	if err != nil {
		return nil, fmt.Errorf("postgres asset search: %w", err)
	}

	out := make([]scriptports.AssetSearchHit, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.AssetID) == "" {
			continue
		}
		out = append(out, scriptports.AssetSearchHit{
			AssetID: result.AssetID,
			Name:    result.Name,
			Score:   result.Score,
			Source:  result.Source,
		})
	}
	return out, nil
}

// clipsNameSearchAdapter bridges the lightweight script clip-discovery
// endpoint to MediaSearcher.SearchClipsByName. PostgreSQL owns both lookup and
// media identity; SQLite media_assets is deliberately absent from this path.
type clipsNameSearchAdapter struct {
	searcher *pgmedia.MediaSearcher
}

func newClipsNameSearchAdapter(db *sql.DB) *clipsNameSearchAdapter {
	if db == nil {
		return nil
	}
	return &clipsNameSearchAdapter{searcher: pgmedia.NewMediaSearcher(db)}
}

func (a *clipsNameSearchAdapter) SearchByName(ctx context.Context, query string, limit int) ([]scriptapi.ClipSearchHit, error) {
	if a == nil || a.searcher == nil {
		return nil, nil
	}
	hits, err := a.searcher.SearchClipsByName(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]scriptapi.ClipSearchHit, 0, len(hits))
	for _, hit := range hits {
		driveLink := ""
		if hit.DriveFileID != "" {
			driveLink = "https://drive.google.com/file/d/" + hit.DriveFileID + "/view"
		}
		out = append(out, scriptapi.ClipSearchHit{
			ID:        hit.AssetID,
			Name:      hit.Name,
			Source:    hit.Source,
			DriveLink: driveLink,
		})
	}
	return out, nil
}

var (
	_ usecase.SemanticSearchPort  = (*postgresSemanticSearchPort)(nil)
	_ scriptports.AssetSearchPort = (*postgresAssetSearchPort)(nil)
	_ scriptapi.ClipSearcher      = (*clipsNameSearchAdapter)(nil)
)
