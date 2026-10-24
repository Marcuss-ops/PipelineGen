// Package stockintelligence — adapters.go owns the provider-registry
// fallback surface. The local (Qdrant/SQLite) adapters are REMOVED
// post Postgres-media-cutover: the single retrieval plane is
// PostgresLocalSearchAdapter + PostgresAssetHydrator (adapters_postgres.go)
// against the PostgreSQL SSOT (pgvector HNSW 768d).
package stockintelligence

import (
	"context"
	"errors"
	"fmt"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ErrProviderUnavailable is the typed signal that a provider cannot serve AT
// ALL in this composition (structurally unregistered, or a not-registered
// dispatch error): issuing the query is pure waste. It is deliberately
// distinct from a transient search failure — only this condition lets the
// resolver skip the provider fallback; every other error stays fail-closed.
var ErrProviderUnavailable = errors.New("stockintelligence: provider unavailable")

// RegistrySearch is the provider-registry surface used only for resolver
// fallback. It keeps provider discovery behind one typed boundary.
type RegistrySearch interface {
	Search(context.Context, string, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error)
}

// providerAvailabilityLookup is the optional registry capability used to
// probe structural provider availability BEFORE issuing a query. The
// concrete VidRush provider registry satisfies it; test fakes that do not
// keep the legacy dispatch path.
type providerAvailabilityLookup interface {
	Provider(name string) (scriptports.VidRushAssetProvider, error)
}

type RegistryProviderClient struct{ Registry RegistrySearch }

func (a RegistryProviderClient) SearchProvider(ctx context.Context, query string, limit int) ([]Candidate, error) {
	if a.Registry == nil {
		return nil, fmt.Errorf("stockintelligence: provider registry is not configured")
	}
	// Quick guard (waste-cut 2026-10-24): a provider that is not registered
	// in this composition can never answer — probe once and fail with the
	// typed unavailability signal instead of issuing the query.
	if lookup, ok := a.Registry.(providerAvailabilityLookup); ok {
		if _, err := lookup.Provider(scriptpkg.VidRushProviderArtlist); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
		}
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
