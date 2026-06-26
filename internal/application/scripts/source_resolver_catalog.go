// Package scripts — source_resolver_catalog.go resolves SourceCatalog
// sources into a ResolvedSource. It searches the local media catalog
// for matching clips, then uses ClipSourceBuilder to build context.
package scripts

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// CatalogSourceResolver resolves SourceCatalog sources by searching
// the local media catalog, fetching matching clips, and building
// ClipEvidence via ClipSourceBuilder.
type CatalogSourceResolver struct {
	catalogSearch appsearch.LocalCatalogPort
	clipBuilder   *ClipSourceBuilder
	log           *zap.Logger
}

// NewCatalogSourceResolver creates a CatalogSourceResolver.
// catalogSearch must be non-nil.
func NewCatalogSourceResolver(
	catalogSearch appsearch.LocalCatalogPort,
	clipBuilder *ClipSourceBuilder,
	log *zap.Logger,
) *CatalogSourceResolver {
	return &CatalogSourceResolver{
		catalogSearch: catalogSearch,
		clipBuilder:   clipBuilder,
		log:           log,
	}
}

// Resolve searches the catalog and builds a ResolvedSource.
func (r *CatalogSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, itemID string) (*scriptpkg.ResolvedSource, error) {
	if r == nil || r.catalogSearch == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: itemID,
			Reason: "catalog source resolver: catalog search service not configured",
		}
	}

	query := strings.TrimSpace(src.Query)
	if query == "" {
		return nil, &scriptpkg.NoSourceError{
			ItemID: itemID,
			Reason: "catalog source requires a query",
		}
	}

	limit := src.MaxClips
	if limit <= 0 {
		limit = 10
	}
	minCoverage := src.MinCoverage

	start := time.Now()

	// Phase 1: search catalog.
	results, err := r.catalogSearch.SearchAll(ctx, query)
	if err != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceCatalog,
			Query:       query,
			ResultCount: 0,
			Inner:       fmt.Errorf("catalog search failed: %w", err),
		}
	}

	// Deduplicate and collect clip IDs.
	seen := make(map[string]struct{}, limit)
	clipIDs := make([]string, 0, limit)
	searchItems := make([]scriptpkg.SearchResultItem, 0, limit)
	for _, result := range results {
		id := strings.TrimSpace(result.ID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		clipIDs = append(clipIDs, id)
		searchItems = append(searchItems, scriptpkg.SearchResultItem{
			ClipID: id,
			Name:   result.Name,
			Score:  result.Score,
			Source: "catalog",
		})
		if len(clipIDs) >= limit {
			break
		}
	}

	// Check coverage if requested.
	if minCoverage > 0 {
		coverage := float64(len(clipIDs)) / float64(limit)
		if coverage < minCoverage {
			return nil, &scriptpkg.SourceResolutionError{
				SourceType:  scriptpkg.SourceCatalog,
				Query:       query,
				ResultCount: len(clipIDs),
				Inner:       fmt.Errorf("catalog coverage %.2f below required minimum %.2f", coverage, minCoverage),
			}
		}
	}

	if len(clipIDs) == 0 {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceCatalog,
			Query:       query,
			ResultCount: 0,
			Inner:       fmt.Errorf("no catalog clips found for query %q", query),
		}
	}

	// Phase 2: build clip context.
	if r.clipBuilder == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: itemID,
			Reason: "catalog source resolver: ClipSourceBuilder not configured",
		}
	}

	opts := &ClipGenerationOptions{
		TranscriptPolicy: src.TranscriptPolicy,
		OrderingStrategy: src.OrderingStrategy,
	}
	pack, plan, sourceText, buildErr := r.clipBuilder.BuildClipContext(ctx, clipIDs, opts)
	if buildErr != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceCatalog,
			Query:       query,
			ResultCount: len(clipIDs),
			Inner:       fmt.Errorf("clip context build failed: %w", buildErr),
		}
	}

	evidence := BuildClipEvidence(pack, sourceText)
	title := ""
	if plan != nil {
		title = plan.Title
	}
	if title == "" {
		title = query
	}
	fingerprint := computeSourceFingerprint(src, evidence)

	if r.log != nil {
		r.log.Info("catalog source resolved",
			zap.String("query", query),
			zap.Int("results", len(results)),
			zap.Int("selected", len(clipIDs)),
			zap.Int64("elapsed_ms", time.Since(start).Milliseconds()))
	}

	return &scriptpkg.ResolvedSource{
		Type:          scriptpkg.SourceCatalog,
		Topic:         title,
		Title:         title,
		SourceText:    sourceText,
		ClipEvidence:  evidence,
		SearchResults: searchItems,
		Fingerprint:   fingerprint,
	}, nil
}
