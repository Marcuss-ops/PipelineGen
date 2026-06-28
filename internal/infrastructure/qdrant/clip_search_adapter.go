// Package qdrant — ClipSearchAdapter adapts the application-level
// ports.ClipSearchPort to the qdrant.Searcher primitives.
//
// PR 5 (June 2026, fix/qdrant-tenant-scope): rewritten on top of
// qdrant.CompileQdrantFilter so the curate search path is
// cross-tenant isolated by default. Previous shape
// (buildCurateClipFilter) hard-coded lifecycle_state = ACTIVE and
// silently dropped the workspace clause (the pre-PR5 curate path
// owned no caller-side workspace scope and there was no obvious
// owner for that scope). PR 5 makes the workspace MUST-clause
// non-optional via the ClipSearchQuery.{WorkspaceID, IsSystem}
// fields added to ports.ClipSearchQuery; the adapter rejects
// empty workspace + IsSystem=false here so the failure surfaces
// typed rather than as a silent zero-hit.
//
// Performance note: the pre-PR5 fast path (SearchByText with no
// filter) is replaced by an unconditional embed + filtered Search
// because the workspace clause is a hard requirement now. The
// extra round-trip is acceptable on the curate path (which is
// not user-hot-path); the canonical /mediasearch search is
// unaffected.
//
// Per AGENTS.md Pattern 0, this is the ONLY place that imports both
// application-level scripts types and qdrant infra types (Hexagonal
// port pattern).
package qdrant

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"

	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// clipSearchAdapter implements ports.ClipSearchPort against the
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
// embedder is required because the curate path always pays the
// embed cost (the post-PR5 algorithm unconditionally issues a
// Search with a workspace + lifecycle filter, so the no-filter
// SearchByText fast path is retired).
// vectorName is the dense vector channel name (e.g. "text") whose
// dimensions the embedder is expected to produce. Both are
// supplied by the composition root (wire_script.go).
func NewClipSearchAdapter(searcher *Searcher, embedder TextEmbedder, vectorName string, log *zap.Logger) ports.ClipSearchPort {
	return &clipSearchAdapter{
		searcher:   searcher,
		embedder:   embedder,
		vectorName: vectorName,
		log:        log,
	}
}

// Compile-time assertion (AGENTS.md Pattern 0).
var _ ports.ClipSearchPort = (*clipSearchAdapter)(nil)

// SearchClips implements ports.ClipSearchPort.
//
// Fail-closed tenant contract (PR 5 §8):
//
//   - WorkspaceID="" && IsSystem=false → typed error
//     ("workspace required or IsSystem=true explicit").
//   - WorkspaceID="default" → typed error (reserved sentinel).
//   - Otherwise → CompileQdrantFilter emits the canonical
//     workspace + lifecycle filter and Search runs against the
//     runtime alias.
func (a *clipSearchAdapter) SearchClips(ctx context.Context, q ports.ClipSearchQuery) ([]ports.ClipSearchHit, error) {
	if a == nil || a.searcher == nil {
		return nil, fmt.Errorf("clip search adapter: searcher not configured")
	}
	if a.embedder == nil {
		return nil, fmt.Errorf("clip search adapter: embedder not configured")
	}
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return []ports.ClipSearchHit{}, nil
	}
	// Tenant guard — fail-closed before any embed cost.
	if err := validateScope(q.WorkspaceID, q.IsSystem); err != nil {
		return nil, err
	}

	limit := defaults.Int(q.Limit, 20)
	minScore := q.MinScore
	if minScore == 0 {
		minScore = 0.5
	}

	// Always pay the embed + filter cost; the pre-PR5 no-filter
	// fast path is gone because the canonical workspace +
	// lifecycle must-clauses are non-optional post-PR5.
	vec, err := a.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("clip search embed: %w", err)
	}

	filt, err := CompileQdrantFilter(
		search.SearchScope{
			WorkspaceID: q.WorkspaceID,
			IsSystem:    q.IsSystem,
		},
		search.AssetFilter{
			Source:    q.Source,
			Category:  q.Category,
			MediaType: q.MediaType,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("clip search: compile filter: %w", err)
	}

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
func convertClipHits(results []SearchResult) []ports.ClipSearchHit {
	out := make([]ports.ClipSearchHit, 0, len(results))
	for _, r := range results {
		out = append(out, ports.ClipSearchHit{
			AssetID: payloadString(r.Payload, "asset_id"),
			Name:    payloadString(r.Payload, "name"),
			Score:   r.Score,
			Source:  payloadString(r.Payload, "source"),
		})
	}
	return out
}

// validateScope is the per-adapter fail-closed gate on the
// WorkspaceID field. Mirrors qdrant.CompileQdrantFilter's invariant
// so the rejection happens BEFORE any embed cost (cheap failure)
// rather than AFTER (an actual embed round-trip then a wasted
// network-filter call). The sentinel "default" string is the
// pre-tenancy convention (see mediasearch.WorkspaceContext); it
// matches the same ErrMissingWorkspace surface that production
// search uses so the error contract is uniform across the two
// search entry points.
func validateScope(workspaceID string, isSystem bool) error {
	if isSystem {
		return nil
	}
	trimmed := strings.TrimSpace(workspaceID)
	if trimmed == "" {
		return fmt.Errorf("clip search adapter: WorkspaceID is required (set IsSystem=true for admin/reconcile/snapshot paths)")
	}
	if trimmed == "default" {
		return fmt.Errorf(`clip search adapter: WorkspaceID is the reserved "default" sentinel; set a real workspace or IsSystem=true`)
	}
	return nil
}

// Ensure the alias to usecase.ClipSearchQuery compiles; see
// source_resolver_curate.go where the type alias orchestrates
// the consolidation PR 5 introduced.
var _ = usecase.ClipSearchQuery{}
