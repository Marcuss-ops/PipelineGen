// Package stockintelligence — adapters.go owns the provider-registry
// fallback surface. The local (Qdrant/SQLite) adapters are REMOVED
// post Postgres-media-cutover: the single retrieval plane is
// PostgresLocalSearchAdapter + PostgresAssetHydrator (adapters_postgres.go)
// against the PostgreSQL SSOT (pgvector HNSW 768d).
package stockintelligence

import (
	"context"
	"fmt"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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
