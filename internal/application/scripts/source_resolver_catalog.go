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
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"

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
//
// PR 4 (June 2026): resolutionContext is threaded into
// ClipGenerationOptions.Language/Tone/Model/Style/TargetWords.
// Catalog hits don't carry language context so resolutionContext
// is the canonical source of truth.
func (r *CatalogSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	if r == nil || r.catalogSearch == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "catalog source resolver: catalog search service not configured",
		}
	}

	query := strings.TrimSpace(src.Query)
	if query == "" {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
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
	if minCoverage > 0 && limit > 0 {
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

	// Phase 2: build clip context via shared hydration helper.
	if r.clipBuilder == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "catalog source resolver: ClipSourceBuilder not configured",
		}
	}

	opts := buildSearchClipOpts(src)
	// PR 4: thread operator-side traits from resolutionContext.
	opts.Language = resCtx.Language
	opts.Title = resCtx.Title
	opts.Tone = resCtx.Tone
	opts.Model = resCtx.Model
	opts.Style = resCtx.Style
	opts.TargetWords = resCtx.TargetWords

	resolved, err := buildResolvedClipSource(ctx, r.clipBuilder, src, resolvedClipParams{
		sourceType:    scriptpkg.SourceCatalog,
		query:         query,
		clipIDs:       clipIDs,
		opts:          opts,
		titleFallback: textutil.FirstNonEmpty(resCtx.Title, query),
		startTime:     start,
	}, r.log)
	if err != nil {
		return nil, err
	}

	resolved.SearchResults = searchItems
	return resolved, nil
}
