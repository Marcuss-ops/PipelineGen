// Package qdrant — SemanticAssetSearchAdapter is the canonical
// unified concrete that subsumes the legacy ClipSearchAdapter
// (curate path) and StockSearchAdapter (visual_planning
// postprocessor) per PR-POSTPROCESSOR-UNIFICATION-PHASE-4
// (August 2026).
//
// godlike/06 SSOT (one canonical owner per fact):
// SemanticAssetSearchAdapter is the SOLE canonical owner of the
// Qdrant-side adapter for AssetSearchPort / ClipSearchPort /
// StockSearchPort. The pre-PR-4 legacy structs
// (clipSearchAdapter + stockSearchAdapter) are retired — their
// responsibilities merged here per KindAsset-discriminated
// per-path behavior.
//
// Split topology (godlike/06 SSOT one-canonical-owner-per-fact):
//   - semantic_asset_search_adapter.go — struct + ctors + SearchAssets + per-path impls + validateScope
//   - semantic_asset_search_legacy.go  — SearchClips + SearchStock (7-day backward-compat wrappers)
//   - semantic_asset_search_convert.go — convertAssetHitsByKind + convertClipAssetHits + convertStockAssetHits
package search

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// semanticAssetSearchAdapter is the canonical unified concrete
// that implements AssetSearchPort (canonical), ClipSearchPort
// (legacy embedded), and StockSearchPort (legacy embedded) per
// the discriminant in `kind`.
//
// A single struct + KindAsset field is the canonical Go-idiomatic
// soft-migration shape: callers see compile-time pins for all 3
// ports on a single value, runtime guards on the legacy methods
// make cross-kind call attempts fail-closed, and the unified
// SearchAssets branches on `kind` exactly once.
type semanticAssetSearchAdapter struct {
	searcher   *Searcher
	embedder   TextEmbedder
	vectorName string
	kind       KindAsset
	assetStore indexing.AssetStore
	log        *zap.Logger
}

// NewSemanticAssetSearchAdapter constructs the canonical unified
// adapter for the supplied `kind`. embedder is required because
// both per-path implementations pay the embed cost (the post-PR-5
// algorithm unconditionally issues a Search with a filter on the
// curate path; the post-PR-4 algorithm also unconditionally issues
// a Search on the stock path). vectorName is the dense vector
// channel name (e.g. "text") whose dimensions the embedder is
// expected to produce. Both are supplied by the composition root.
//
// Returns the canonical ports.AssetSearchPort as the primary
// return type (callers composing the canonical port should ignore
// the legacy methods during the 7-day soak).
//
// Per godlike/06 SSOT (one canonical owner per fact):
// NewSemanticAssetSearchAdapter is the SOLE canonical constructor;
// NewClipSearchAdapter + NewStockSearchAdapter are thin wrappers
// around this constructor that exist ONLY for the 7-day soak
// (forward-pointer PR-CLIPS-STOCK-PORT-RETIRE retires them).
func NewSemanticAssetSearchAdapter(
	searcher *Searcher,
	embedder TextEmbedder,
	vectorName string,
	kind KindAsset,
	log *zap.Logger,
) ports.AssetSearchPort {
	return NewSemanticAssetSearchAdapterWithStore(searcher, embedder, vectorName, kind, nil, log)
}

// NewSemanticAssetSearchAdapterWithStore constructs the semantic asset
// search adapter with the canonical SQLite asset store. Qdrant supplies
// only ranked identities; the store supplies the fields returned to the
// caller. The legacy constructor remains available for non-API compatibility
// tests and callers that only exercise preflight guards.
func NewSemanticAssetSearchAdapterWithStore(
	searcher *Searcher,
	embedder TextEmbedder,
	vectorName string,
	kind KindAsset,
	assetStore indexing.AssetStore,
	log *zap.Logger,
) ports.AssetSearchPort {
	return &semanticAssetSearchAdapter{
		searcher:   searcher,
		embedder:   embedder,
		vectorName: vectorName,
		kind:       kind,
		assetStore: assetStore,
		log:        log,
	}
}

// Compile-time assertions (AGENTS.md Pattern 0):
//
//   - AssetSearchPort: the canonical Port per PR-POSTPROCESSOR-UNIFICATION-PHASE-3.
//   - ClipSearchPort: legacy embedded Port satisfied via SearchClips wrapper + SearchAssets.
//   - StockSearchPort: legacy embedded Port satisfied via SearchStock wrapper + SearchAssets.
//
// A future drift in any of the 3 port signatures surfaces as a
// build failure here (NOT a runtime panic on a production
// caller). This is the canonical PinDiscipline per AGENTS.md
// Pattern 0.
var (
	_ ports.AssetSearchPort = (*semanticAssetSearchAdapter)(nil)
)

// SearchAssets implements ports.AssetSearchPort (canonical).
//
// This is the canonical method. It branches ONCE on `a.kind` to
// select the per-path strategy (clip vs stock). The legacy methods
// SearchClips + SearchStock are thin wrappers that convert from
// the legacy query types, call SearchAssets, and convert the
// result back.
//
// KindDiscriminationLockedByRuntimeGuards: the legacy wrappers
// enforce `a.kind == <this-kind>` at runtime; the canonical
// SearchAssets is the only place that reads `a.kind` for behavior
// selection. So adding a third kind is a godlike/06 SSOT expansion
// (one canonical owner per fact) — see kind.go godoc.
func (a *semanticAssetSearchAdapter) SearchAssets(ctx context.Context, q ports.AssetSearchQuery) ([]ports.AssetSearchHit, error) {
	// Canonical nil-receiver guard FIRST (godlike/07 NO-FAKE-AVAILABILITY):
	// never deref a.kind or a.searcher before the guard. The error
	// message uses a static shape ("nil receiver") so the typed-
	// envelope is meaningful without a per-kind dispatch.
	if a == nil {
		return nil, fmt.Errorf("semantic search adapter: nil receiver")
	}
	// Fast-path: empty query short-circuits BEFORE the searcher AND
	// embedder guards (you don't need a wired searcher or embedder
	// to return [] for an empty query; this is a cheap pre-flight
	// that mirrors the legacy clip+stock patterns).
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return []ports.AssetSearchHit{}, nil
	}
	if a.searcher == nil {
		return nil, fmt.Errorf("%s search adapter: searcher not configured", a.kind)
	}

	// Strategy dispatch.
	switch a.kind {
	case KindClip:
		return a.searchAssetsClip(ctx, q, query)
	case KindStock:
		return a.searchAssetsStock(ctx, q, query)
	default:
		return nil, fmt.Errorf("semantic search adapter: unknown kind=%d (canonical SSOT is KindClip|KindStock)", a.kind)
	}
}

// searchAssetsClip is the per-path implementation for KindClip.
//
// Fail-closed tenant contract (PR 5 June 2026, fix/qdrant-tenant-scope):
//   - WorkspaceID="" && IsSystem=false → typed error.
//   - WorkspaceID="default" → typed error (reserved sentinel).
//   - Otherwise → CompileQdrantFilter emits the canonical
//     workspace + lifecycle filter and Search runs against the
//     runtime alias.
//
// RequireActiveLifecycle is REDUNDANT on the clip path
// (lifecycle=ACTIVE is already enforced by CompileQdrantFilter);
// the field is accepted silently to keep the canonical query type
// uniform across kinds.
func (a *semanticAssetSearchAdapter) searchAssetsClip(ctx context.Context, q ports.AssetSearchQuery, query string) ([]ports.AssetSearchHit, error) {
	// Tenant guard — fail-closed before any embed cost.
	if err := validateScope(q.WorkspaceID, q.IsSystem); err != nil {
		return nil, err
	}
	if a.embedder == nil {
		return nil, fmt.Errorf("clip search adapter: embedder not configured")
	}

	// Per-kind defaults pin the canonical invariants:
	//   - clip:  Limit=20, MinScore=0.5 (matches pre-PR-4 clipSearchAdapter)
	//   - stock: Limit=5,  MinScore=0.3 (matches pre-PR-4 stockSearchAdapter)
	limit := 20
	if q.Limit > 0 {
		limit = q.Limit
	}
	minScore := q.MinScore
	if minScore == 0 {
		minScore = 0.5
	}

	vec, err := a.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("clip search embed: %w", err)
	}

	filt, err := CompileQdrantFilter(
		appsearch.SearchScope{
			WorkspaceID: q.WorkspaceID,
			IsSystem:    q.IsSystem,
		},
		appsearch.AssetFilter{
			Source:                q.Source,
			Category:              q.Category,
			MediaType:             q.MediaType,
			FolderNormalizedGroup: q.FolderNormalizedGroup,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("clip search: compile filter: %w", err)
	}

	results, err := a.searcher.Search(ctx, schema.SearchRequest{
		QueryVector: vec,
		VectorName:  a.vectorName,
		Limit:       limit,
		MinScore:    minScore,
		Filter:      filt,
	})
	if err != nil {
		return nil, fmt.Errorf("clip search: %w", err)
	}
	return a.hydrateAssetSearchHits(ctx, results)
}

// searchAssetsStock is the per-path implementation for KindStock.
//
// Stock-specific contract (preserved across the 7-day soak):
//   - Source is hard-coded to "stock" (vs clip's configurable Source).
//   - RequireActiveLifecycle is FORCED to true (vs clip's silent drop).
//   - MinScore defaults to 0.3 (vs 0.5 for clips).
//   - Limit defaults to 5 (vs 20 for clips).
//   - DriveLink is populated from the payload (vs empty for clips per QDRANT-001).
//   - No workspace/tenant guard (stock is admin/reconcile path only).
func (a *semanticAssetSearchAdapter) searchAssetsStock(ctx context.Context, q ports.AssetSearchQuery, query string) ([]ports.AssetSearchHit, error) {
	if a.embedder == nil {
		return nil, fmt.Errorf("stock search adapter: embedder not configured")
	}

	// Per-kind defaults (stock: Limit=5, MinScore=0.3 — matches
	// pre-PR-4 stockSearchAdapter).
	limit := 5
	if q.Limit > 0 {
		limit = q.Limit
	}
	minScore := q.MinScore
	if minScore == 0 {
		minScore = 0.3
	}

	vec, err := a.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("stock search embed: %w", err)
	}

	filt, err := CompileQdrantFilter(
		appsearch.SearchScope{IsSystem: true},
		appsearch.AssetFilter{
			Source:         "stock",
			LifecycleState: []string{"ACTIVE"},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("stock search: compile filter: %w", err)
	}

	results, err := a.searcher.Search(ctx, schema.SearchRequest{
		QueryVector: vec,
		VectorName:  a.vectorName,
		Limit:       limit,
		MinScore:    minScore,
		Filter:      filt,
	})
	if err != nil {
		return nil, fmt.Errorf("stock search: %w", err)
	}
	return a.hydrateAssetSearchHits(ctx, results)
}

// hydrateAssetSearchHits keeps the Qdrant ranking while resolving every
// returned field from SQLite. A missing/tombstoned row is omitted because
// Qdrant is a derived projection and may lag behind the canonical registry.
func (a *semanticAssetSearchAdapter) hydrateAssetSearchHits(ctx context.Context, results []schema.SearchResult) ([]ports.AssetSearchHit, error) {
	if len(results) == 0 {
		return []ports.AssetSearchHit{}, nil
	}
	if a.assetStore == nil {
		return nil, fmt.Errorf("semantic search hydration: SQLite asset store not configured")
	}

	out := make([]ports.AssetSearchHit, 0, len(results))
	for _, result := range results {
		assetID := payloadString(result.Payload, "asset_id")
		if assetID == "" {
			continue
		}
		asset, err := a.assetStore.FetchAsset(ctx, assetID)
		if err != nil {
			if errors.Is(err, indexing.ErrAssetNotFound) {
				continue
			}
			return nil, fmt.Errorf("hydrate asset %q from SQLite: %w", assetID, err)
		}
		if asset == nil || asset.ID != assetID || asset.DeletedAt != "" {
			continue
		}
		out = append(out, ports.AssetSearchHit{
			AssetID: asset.ID,
			Name:    asset.Name,
			Score:   result.Score,
			Source:  asset.Source,
			// DriveLink is canonical SQLite data. It is never read
			// from the Qdrant payload, even for the stock path.
			DriveLink: asset.DriveLink,
		})
	}
	return out, nil
}

// validateScope is the per-adapter fail-closed gate on the
// WorkspaceID field. Called ONLY from searchAssetsClip — stock
// is admin/reconcile only and does NOT call validateScope.
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
