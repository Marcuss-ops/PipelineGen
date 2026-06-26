// Package scripts — source_resolver_search.go resolves SourceSearch
// sources into a ResolvedSource. It performs semantic search via the
// ClipSearchPort, then uses ClipSourceBuilder to build context.
package scripts

import (
	"context"
	"fmt"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"

	"go.uber.org/zap"
)

// SearchSourceResolver resolves SourceSearch sources by performing
// semantic clip search and building ClipEvidence.
type SearchSourceResolver struct {
	clipSearch  ClipSearchPort
	clipBuilder *ClipSourceBuilder
	log         *zap.Logger
}

// NewSearchSourceResolver creates a SearchSourceResolver.
// clipSearch must be non-nil.
func NewSearchSourceResolver(
	clipSearch ClipSearchPort,
	clipBuilder *ClipSourceBuilder,
	log *zap.Logger,
) *SearchSourceResolver {
	return &SearchSourceResolver{
		clipSearch:  clipSearch,
		clipBuilder: clipBuilder,
		log:         log,
	}
}

// Resolve performs semantic search and builds a ResolvedSource.
func (r *SearchSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, itemID string) (*scriptpkg.ResolvedSource, error) {
	if r == nil || r.clipSearch == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: itemID,
			Reason: "search source resolver: ClipSearchPort not configured",
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

	start := time.Now()

	// Phase 1: semantic search.
	hits, err := r.clipSearch.SearchClips(ctx, ClipSearchQuery{
		Query:     query,
		Source:    "",
		Category:  "",
		MediaType: "",
		Limit:     limit,
		MinScore:  ptrutil.DerefOr(src.MinQualityScore, 0.0),
	})
	if err != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceSearch,
			Query:       query,
			ResultCount: 0,
			Inner:       fmt.Errorf("semantic search failed: %w", err),
		}
	}

	if len(hits) == 0 {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceSearch,
			Query:       query,
			ResultCount: 0,
			Inner:       fmt.Errorf("no search results for query %q", query),
		}
	}

	// Deduplicate and convert hits to clip IDs.
	seen := make(map[string]struct{}, len(hits))
	clipIDs := make([]string, 0, len(hits))
	searchItems := make([]scriptpkg.SearchResultItem, 0, len(hits))
	for _, hit := range hits {
		if _, dup := seen[hit.AssetID]; dup {
			continue
		}
		seen[hit.AssetID] = struct{}{}
		clipIDs = append(clipIDs, hit.AssetID)
		searchItems = append(searchItems, scriptpkg.SearchResultItem{
			ClipID: hit.AssetID,
			Name:   hit.Name,
			Score:  hit.Score,
			Source: hit.Source,
		})
	}

	// Phase 2: build clip context.
	if r.clipBuilder == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: itemID,
			Reason: "search source resolver: ClipSourceBuilder not configured",
		}
	}

	opts := &ClipGenerationOptions{
		TranscriptPolicy: src.TranscriptPolicy,
		OrderingStrategy: src.OrderingStrategy,
	}
	pack, plan, sourceText, buildErr := r.clipBuilder.BuildClipContext(ctx, clipIDs, opts)
	if buildErr != nil {
		return nil, &scriptpkg.SourceResolutionError{
			SourceType:  scriptpkg.SourceSearch,
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
		r.log.Info("search source resolved",
			zap.String("query", query),
			zap.Int("hits", len(hits)),
			zap.Int("selected", len(clipIDs)),
			zap.Int64("elapsed_ms", time.Since(start).Milliseconds()))
	}

	return &scriptpkg.ResolvedSource{
		Type:          scriptpkg.SourceSearch,
		Topic:         title,
		Title:         title,
		SourceText:    sourceText,
		ClipEvidence:  evidence,
		SearchResults: searchItems,
		Fingerprint:   fingerprint,
	}, nil
}
