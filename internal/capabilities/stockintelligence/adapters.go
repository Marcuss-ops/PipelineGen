package stockintelligence

import (
	"context"
	"fmt"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
)

type QdrantLocalSearchAdapter struct {
	Searcher   *search.Searcher
	Embedder   search.TextEmbedder
	VectorName string
}

// SQLiteBatchStore is the narrow SQLite truth surface required for local
// candidate hydration. The concrete imagesregistry store satisfies it.
type SQLiteBatchStore interface {
	BatchGetByIDs(context.Context, []string) (map[string]*asset.Details, error)
}

// SQLiteAssetHydrator adapts the canonical media_assets table to the local
// resolver. Qdrant supplies IDs/scores; SQLite supplies the label truth.
type SQLiteAssetHydrator struct{ Store SQLiteBatchStore }

func (a SQLiteAssetHydrator) Hydrate(ctx context.Context, ids []string) (map[string]string, error) {
	if a.Store == nil {
		return nil, fmt.Errorf("stockintelligence: SQLite asset store is not configured")
	}
	rows, err := a.Store.BatchGetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for id, details := range rows {
		if details == nil || details.Asset == nil {
			continue
		}
		label := details.Asset.SearchText
		if label == "" {
			label = details.Asset.Name
		}
		if label == "" {
			label = details.Asset.Filename
		}
		if label != "" {
			out[id] = label
		}
	}
	return out, nil
}

// RegistrySearch is the provider-registry surface used only for resolver
// fallback. It keeps provider discovery behind one typed boundary.
type RegistrySearch interface {
	Search(context.Context, string, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error)
}

type RegistryProviderClient struct{ Registry RegistrySearch }

func (a RegistryProviderClient) SearchProvider(ctx context.Context, query string, limit int) ([]Candidate, error) {
	if a.Registry == nil {
		return nil, fmt.Errorf("stockintelligence: provider registry is not configured")
	}
	rows, err := a.Registry.Search(ctx, scriptpkg.VidRushProviderArtlist, scriptports.VidRushSearchRequest{Query: query, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, Candidate{AssetID: row.AssetID, Label: row.Entity, GenericSimilarity: float32(row.RelevanceScore), OwnerSegmentID: row.SegmentID, Source: "provider"})
	}
	return out, nil
}

func (a QdrantLocalSearchAdapter) SearchLocal(ctx context.Context, query string, limit int) ([]Candidate, error) {
	if a.Searcher == nil || a.Embedder == nil {
		return nil, fmt.Errorf("stockintelligence: local search not configured")
	}
	if limit <= 0 {
		limit = 20
	}
	hits, err := a.Searcher.SearchByText(ctx, query, a.Embedder, a.VectorName, limit, 0)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(hits))
	for _, hit := range hits {
		out = append(out, Candidate{AssetID: hit.AssetID, GenericSimilarity: float32(hit.Score), Source: "local"})
	}
	return out, nil
}
