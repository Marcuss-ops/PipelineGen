// Package qdrant — ClipSearchAdapter adapts the application-level
// scripts.ClipSearchPort to the qdrant.Searcher primitives
// (SearchByText for the no-filter fast path; Search with explicit
// filter must-clauses for filtered queries).
//
// PJ-CURATE-1 (June 2026): the previous MediaCurator fell back to
// text-only whenever req.HintClipIDs was empty. This adapter
// reinstates an opt-in semantic-search leg (caller passes
// allows the curate path to produce
// curated clip IDs without caller-side seeding.
//
// Per AGENTS.md Pattern 0, this is the ONLY place that imports
// both application-level scripts types and qdrant infra types
// (Hexagonal port pattern).
package qdrant

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// clipSearchAdapter implements scripts.ClipSearchPort against the
// canonical qdrant.Searcher. Embedding is supplied by the caller so
// the adapter has no Ollama / HTTP-text-embedder / Python-script
// dependency directly — composition root (wire_script.go) chooses
// the implementation.
type clipSearchAdapter struct {
	searcher   *Searcher
	embedder   TextEmbedder
	vectorName string
	log        *zap.Logger
}

// NewClipSearchAdapter constructs the ClipSearchPort implementation.
// embedder is required (SearchByText is embed-then-ANN; the filtered
// path embeds then calls Search with explicit must-clauses).
// vectorName is the dense vector channel name (e.g. "text") whose
// dimensions the embedder is expected to produce. Both are
// supplied by the composition root (wire_script.go).
func NewClipSearchAdapter(searcher *Searcher, embedder TextEmbedder, vectorName string, log *zap.Logger) scripts.ClipSearchPort {
	return &clipSearchAdapter{
		searcher:   searcher,
		embedder:   embedder,
		vectorName: vectorName,
		log:        log,
	}
}

// Compile-time assertion (AGENTS.md Pattern 0).
var _ scripts.ClipSearchPort = (*clipSearchAdapter)(nil)

// SearchClips implements scripts.ClipSearchPort.
//   - No filter set → SearchByText (embed + ANN in one call, fast path).
//   - Any filter set → embed then Search with explicit must-clauses.
func (a *clipSearchAdapter) SearchClips(ctx context.Context, q scripts.ClipSearchQuery) ([]scripts.ClipSearchHit, error) {
	if a == nil || a.searcher == nil {
		return nil, fmt.Errorf("clip search adapter: searcher not configured")
	}
	if a.embedder == nil {
		return nil, fmt.Errorf("clip search adapter: embedder not configured")
	}
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return []scripts.ClipSearchHit{}, nil
	}
	limit := defaults.Int(q.Limit, 20)
	minScore := q.MinScore
	if minScore == 0 {
		minScore = 0.5
	}
	hasFilter := strings.TrimSpace(q.Source) != "" ||
		strings.TrimSpace(q.Category) != "" ||
		strings.TrimSpace(q.MediaType) != ""

	if !hasFilter {
		// Fast path: SearchByText does embed + ANN in one call.
		results, err := a.searcher.SearchByText(ctx, query, a.embedder, a.vectorName, limit, minScore)
		if err != nil {
			return nil, fmt.Errorf("clip search: %w", err)
		}
		return convertClipHits(results), nil
	}

	// Filtered path: embed + Search with explicit Qdrant filter.
	vec, err := a.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("clip search embed: %w", err)
	}
	filt := buildCurateClipFilter(q.Source, q.Category, q.MediaType)
	results, err := a.searcher.Search(ctx, SearchRequest{
		QueryVector: vec,
		VectorName:  a.vectorName,
		Limit:       limit,
		MinScore:    minScore,
		Filter:      filt,
	})
	if err != nil {
		return nil, fmt.Errorf("clip search: %w", err)
	}
	return convertClipHits(results), nil
}

// convertClipHits maps infra-level SearchResult → app-level
// ClipSearchHit (strips non-port fields).
func convertClipHits(results []SearchResult) []scripts.ClipSearchHit {
	out := make([]scripts.ClipSearchHit, 0, len(results))
	for _, r := range results {
		out = append(out, scripts.ClipSearchHit{
			AssetID: payloadString(r.Payload, "asset_id"),
			Name:    payloadString(r.Payload, "name"),
			Score:   r.Score,
			Source:  payloadString(r.Payload, "source"),
		})
	}
	return out
}

// buildCurateClipFilter matches the worker_search path filter shape:
// "source"/"category"/"media_type" matched value + canonical
// lifecycle_state = ACTIVE. Keeps curate results consistent with the
// canonical /internal/v1/media/search filter contract.
//
// PR 1 — Lifecycle state SSOT (June 2026): the lifecycle filter is
// the canonical ACTIVE match only. Pre-PR1 the waterfall was {active,
// searchable}; both legacy values are pruned by migration 101. The
// delegate to qdrant.buildLifecycleAwareFilter is intentionally
// avoided here to keep this function testable without depending on
// the Search/HybridSearch path — curate is the only fan-out that
// owns no caller-side workspace, so we hardcode the workspace-id
// clause as "not present" and let the canonical builder plumbing be
// exercised from search_adapter.go.
//
// Mirrors search_adapter.go::filter-must construction so curate
// returns the same set of points the search endpoint would.
func buildCurateClipFilter(source, category, mediaType string) map[string]interface{} {
	must := make([]map[string]interface{}, 0, 4)
	if s := strings.TrimSpace(source); s != "" {
		must = append(must, map[string]interface{}{
			"key":   "source",
			"match": map[string]interface{}{"value": s},
		})
	}
	if c := strings.TrimSpace(category); c != "" {
		must = append(must, map[string]interface{}{
			"key":   "category",
			"match": map[string]interface{}{"value": c},
		})
	}
	if m := strings.TrimSpace(mediaType); m != "" {
		must = append(must, map[string]interface{}{
			"key":   "media_type",
			"match": map[string]interface{}{"value": m},
		})
	}
	must = append(must, map[string]interface{}{
		"key":   "lifecycle_state",
		"match": map[string]interface{}{"value": "ACTIVE"},
	})
	return map[string]interface{}{"must": must}
}
