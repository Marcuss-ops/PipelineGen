package wiring

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	imagesregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
)

func semanticBinaryPath(base, name string) string {
	envName := "VELOX_" + strings.ToUpper(name) + "_PATH"
	if configured := strings.TrimSpace(os.Getenv(envName)); configured != "" {
		return configured
	}
	if base != "" {
		candidate := filepath.Join(filepath.Dir(base), name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return base
}

// semanticRegistryProvider adapts the already-composed VidRush registry to
// stockintelligence's provider-second port. It performs discovery only; the
// existing materializer/finalizer remains the sole owner of asset bytes.
type semanticRegistryProvider struct {
	registry *adapters.VidRushAssetProviderRegistry
}

func (p semanticRegistryProvider) SearchProvider(ctx context.Context, query string, limit int) ([]stockintelligence.Candidate, error) {
	if p.registry == nil {
		return nil, fmt.Errorf("semantic provider: registry is nil")
	}
	providers := []string{scriptpkg.VidRushProviderInternetImages, scriptpkg.VidRushProviderArtlist, scriptpkg.VidRushProviderImageGeneration}
	var lastErr error
	for _, name := range providers {
		if _, err := p.registry.Provider(name); err != nil {
			lastErr = err
			continue
		}
		found, err := p.registry.Search(ctx, name, scriptports.VidRushSearchRequest{Query: query, Text: query, Limit: limit})
		if err != nil {
			lastErr = err
			continue
		}
		out := make([]stockintelligence.Candidate, 0, len(found))
		for _, c := range found {
			label := c.Entity
			if label == "" {
				label = c.Query
			}
			out = append(out, stockintelligence.Candidate{AssetID: c.AssetID, Label: label, GenericSimilarity: float32(c.RelevanceScore), Source: name})
		}
		return out, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("semantic provider: no registered fallback provider")
}

// semanticAssetHydrator enforces SQLite truth after a Qdrant hit. Unknown
// IDs are returned without labels and are consequently excluded by the
// stockintelligence resolver instead of being promoted from Qdrant alone.
type semanticAssetHydrator struct {
	store *imagesregistry.AssetStoreSQLite
}

func (h semanticAssetHydrator) Hydrate(ctx context.Context, ids []string) (map[string]string, error) {
	if h.store == nil {
		return nil, fmt.Errorf("semantic hydrator: asset store is nil")
	}
	rows, err := h.store.BatchGetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	labels := make(map[string]string, len(rows))
	for id, details := range rows {
		if details != nil && details.Asset != nil {
			labels[id] = details.Asset.Name
		}
	}
	return labels, nil
}

func buildSemanticStockResolver(registry *adapters.VidRushAssetProviderRegistry, search stockintelligence.LocalSearchPort, store *imagesregistry.AssetStoreSQLite) (scriptgenLocalResolver, error) {
	if registry == nil || search == nil || store == nil {
		return nil, fmt.Errorf("semantic stock resolver: local search, store and registry are required")
	}
	resolver, err := stockintelligence.NewResolver(search, semanticAssetHydrator{store: store}, semanticRegistryProvider{registry: registry}, func(candidates []stockintelligence.Candidate, _ string, _ string, _ []string) (string, error) {
		var winner string
		var score float32
		for _, c := range candidates {
			if c.Label == "" {
				continue
			}
			if winner == "" || c.GenericSimilarity > score {
				winner, score = c.AssetID, c.GenericSimilarity
			}
		}
		return winner, nil
	})
	if err != nil {
		return nil, err
	}
	return resolver, nil
}

// Narrow alias keeps this composition helper independent of the scripts
// capability's concrete resolver type while preserving its port contract.
type scriptgenLocalResolver interface {
	Resolve(context.Context, stockintelligence.ResolveRequest) (stockintelligence.ResolveResult, error)
}
