// Package scripts — source_resolver_search.go resolves SourceSearch
// sources into a ResolvedSource. It performs semantic search via a
// pluggable SemanticSearchPort (backed by Qdrant in production), then
// uses ClipSourceBuilder to build context from the matched clips.
package scripts

import (
	"context"
	"fmt"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// SemanticSearchPort is the narrow interface the Search resolver needs
// to find clips by text query. Production wiring maps this to Qdrant
// hybrid search (dense + sparse + transcript). Test wiring uses a
// fake for deterministic results.
type SemanticSearchPort interface {
	// SearchByText performs semantic/hybrid search and returns
	// matched clip results ordered by relevance (descending).
	SearchByText(ctx context.Context, query string, limit int, language string) ([]SemanticSearchResult, error)
}

// SemanticSearchResult is a single clip match from semantic search.
type SemanticSearchResult struct {
	ClipID string  `json:"clip_id"`
	Name   string  `json:"name"`
	Score  float64 `json:"score"`
}

// SearchSourceResolver resolves SourceSearch sources by performing
// semantic search and building ClipEvidence via ClipSourceBuilder.
//
// TODO(PR-followup): unit tests currently only exercise Phase 1
// (search port error paths); Phase 2 (ClipSourceBuilder context
// assembly) needs a testable fake ClipSourceBuilder. Same gap
// exists in CatalogSourceResolver tests.
type SearchSourceResolver struct {
	search      SemanticSearchPort
	clipBuilder *ClipSourceBuilder
	log         *zap.Logger
}

// NewSearchSourceResolver creates a SearchSourceResolver.
// search and clipBuilder must be non-nil (enforced at registration
// time by wire_script.go).
func NewSearchSourceResolver(
	search SemanticSearchPort,
	clipBuilder *ClipSourceBuilder,
	log *zap.Logger,
) *SearchSourceResolver {
	return &SearchSourceResolver{
		search:      search,
		clipBuilder: clipBuilder,
		log:         log,
	}
}

// Resolve performs semantic search and builds a ResolvedSource
// with ClipEvidence and SearchResults.
func (r *SearchSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, itemID string) (*scriptpkg.ResolvedSource, error) {
	if r == nil || r.search == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: itemID,
			Reason: "search source resolver: semantic search service not configured",
		}
	}

	query := strings.TrimSpace(src.Query)
	if query == "" {
		return nil, &scriptpkg.NoSourceError{
			ItemID: itemID,
			Reason: "search source requires a query",
		}
	}

	limit := src.MaxClips
	if limit <= 0 {
		limit = 10
	}
	minCoverage := src.MinCoverage

	start := time.Now()

	// Phase 1: semantic search.
	results, err := r.search.SearchByText(ctx, query, limit, "")
	if err != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceSearch,
			Query:       query,
			ResultCount: 0,
			Inner:       fmt.Errorf("semantic search failed: %w", err),
		}
	}

	// Deduplicate and collect clip IDs. Defensive — semantic search
	// should not return duplicates, but the identity layer is the
	// single place to enforce uniqueness.
	seen := make(map[string]struct{}, limit)
	clipIDs := make([]string, 0, limit)
	searchItems := make([]scriptpkg.SearchResultItem, 0, limit)
	for _, result := range results {
		id := strings.TrimSpace(result.ClipID)
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
			Source: "semantic",
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
				SourceType:  scriptpkg.SourceSearch,
				Query:       query,
				ResultCount: len(clipIDs),
				Inner:       fmt.Errorf("search coverage %.2f below required minimum %.2f", coverage, minCoverage),
			}
		}
	}

	if len(clipIDs) == 0 {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceSearch,
			Query:       query,
			ResultCount: 0,
			Inner:       fmt.Errorf("no semantic search results for query %q", query),
		}
	}

	// Phase 2: build clip context via shared hydration helper.
	if r.clipBuilder == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: itemID,
			Reason: "search source resolver: ClipSourceBuilder not configured",
		}
	}

	resolved, err := buildResolvedClipSource(ctx, r.clipBuilder, src, resolvedClipParams{
		sourceType:    scriptpkg.SourceSearch,
		query:         query,
		clipIDs:       clipIDs,
		opts:          buildSearchClipOpts(src),
		titleFallback: query,
		startTime:     start,
	}, r.log)
	if err != nil {
		return nil, err
	}

	resolved.SearchResults = searchItems
	return resolved, nil
}
