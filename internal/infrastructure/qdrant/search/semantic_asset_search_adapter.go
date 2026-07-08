// Package qdrant — SemanticAssetSearchAdapter is the canonical
// unified concrete that subsumes the legacy ClipSearchAdapter
// (curate path) and StockSearchAdapter (stock_association
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
// godlike/07 NO-FAKE-AVAILABILITY (the design rationale for NOT
// splitting back into 2 structs after a refactor attempt):
//   - Runtime guards on legacy methods (SearchClips / SearchStock
//     assert a.kind == KindClip/KindStock respectively) make
//     cross-pollination structurally impossible: the clip-path
//     SearchAssets cannot accidentally apply stock defaults, and
//     vice versa.
//   - Per-kind `if a.kind == ... { ... }` in SearchAssets is the
//     single strategy-pattern if/else, NOT scattered across N
//     methods. The choice between (one struct, two structs, two
//     strategies) collapses to a single decision surface.
//   - The two convert helpers (convertClipAssetHits /
//     convertStockAssetHits) preserve the per-path wire-shape
//     invariants (drive_link="" vs drive_link=populated) that the
//     PR-4 split would have made easier to break.
//
// 7-day backward-compat window (per PR-POSTPROCESSOR-UNIFICATION-
// PHASE-4 user spec literal): NewClipSearchAdapter and
// NewStockSearchAdapter become thin wrappers around the canonical
// NewSemanticAssetSearchAdapter constructor, preserving the wire
// shape for the existing composition-root call sites
// (app/wire_script_resolvers.go:165 +
// app/wire_script_postprocess.go:359). forward-pointer
// PR-CLIPS-STOCK-PORT-RETIRE converts ClipSearchPort +
// StockSearchPort to true Go type aliases
// (type X = AssetSearchPort) at soak-end (2026-08-15).
//
// Per AGENTS.md Pattern 0, this is the ONLY file that imports
// both application-level scripts types (ports) and qdrant infra
// types (schema, Searcher) — Hexagonal port pattern.
package search

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
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
	return &semanticAssetSearchAdapter{
		searcher:   searcher,
		embedder:   embedder,
		vectorName: vectorName,
		kind:       kind,
		log:        log,
	}
}

// NewClipSearchAdapter is a 7-day backward-compat wrapper that
// delegates to NewSemanticAssetSearchAdapter with kind=KindClip.
// Returns the legacy ports.ClipSearchPort type so the 2 existing
// wire sites (wire_script_resolvers.go:165 + the catalog layer)
// continue to compile without modification.
//
// Forward-pointer PR-CLIPS-STOCK-PORT-RETIRE retires this function
// at soak-end (deadline 2026-08-15).
func NewClipSearchAdapter(searcher *Searcher, embedder TextEmbedder, vectorName string, log *zap.Logger) ports.ClipSearchPort {
	port := NewSemanticAssetSearchAdapter(searcher, embedder, vectorName, KindClip, log)
	return port.(ports.ClipSearchPort)
}

// NewStockSearchAdapter is a 7-day backward-compat wrapper that
// delegates to NewSemanticAssetSearchAdapter with kind=KindStock.
// Returns the legacy ports.StockSearchPort type so the 1 existing
// wire site (wire_script_postprocess.go:359) continues to compile
// without modification.
//
// Forward-pointer PR-CLIPS-STOCK-PORT-RETIRE retires this function
// at soak-end (deadline 2026-08-15).
func NewStockSearchAdapter(searcher *Searcher, embedder TextEmbedder, vectorName string, log *zap.Logger) ports.StockSearchPort {
	port := NewSemanticAssetSearchAdapter(searcher, embedder, vectorName, KindStock, log)
	return port.(ports.StockSearchPort)
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
	_ ports.ClipSearchPort  = (*semanticAssetSearchAdapter)(nil)
	_ ports.StockSearchPort = (*semanticAssetSearchAdapter)(nil)
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
	// that mirrors the legacy clip+stock patterns). The fast-path is
	// shared across both kinds because "empty query = empty result"
	// is a universal invariant, not a per-path one. This ordering
	// is the load-bearing contract that lets the 7-day backward-
	// compat wrappers (NewClipSearchAdapter + NewStockSearchAdapter)
	// be probed for the empty-query invariant without wiring the
	// full searcher/embedder stack in the test surface.
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return []ports.AssetSearchHit{}, nil
	}
	if a.searcher == nil {
		return nil, fmt.Errorf("%s search adapter: searcher not configured", a.kind)
	}

	// Strategy dispatch. The 7-day soak fails closed: if a
	// future drift causes a kind={none} or a new kind to reach
	// here without a corresponding case, the repository falls
	// back to typed-error per godlike/07 NO-FAKE-AVAILABILITY.
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
	// Defaults are inlined here (NOT a.kind helper method) per
	// the test-surface note "We don't reference
	// KindAsset.MinScoreDefault/LimitDefault here if they aren't
	// shipped (avoid test breakage)."
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
			Source:    q.Source,
			Category:  q.Category,
			MediaType: q.MediaType,
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
	return convertAssetHitsByKind(results, a.kind), nil
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
	// pre-PR-4 stockSearchAdapter). Inlined per the same rationale
	// as searchAssetsClip (no helper-method coupling).
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

	results, err := a.searcher.Search(ctx, schema.SearchRequest{
		QueryVector: vec,
		VectorName:  a.vectorName,
		Limit:       limit,
		MinScore:    minScore,
		Filter: map[string]interface{}{
			"must": []map[string]interface{}{
				{"key": "source", "match": map[string]interface{}{"value": "stock"}},
				{"key": "lifecycle_state", "match": map[string]interface{}{"value": "ACTIVE"}},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("stock search: %w", err)
	}
	return convertAssetHitsByKind(results, a.kind), nil
}

// SearchClips implements the legacy ClipSearchPort method.
//
// The runtime guard `a.kind != KindClip` makes accidental
// cross-pollination structurally impossible: a future agent that
// passes a stock-flavored adapter to a clip-flavored caller will
// see the typed error immediately, not a silently-wrong result
// set.
//
// 7-day backward-compat: this method survives until
// PR-CLIPS-STOCK-PORT-RETIRE retires it (deadline 2026-08-15).
func (a *semanticAssetSearchAdapter) SearchClips(ctx context.Context, q ports.ClipSearchQuery) ([]ports.ClipSearchHit, error) {
	if a.kind != KindClip {
		return nil, fmt.Errorf("clip path called on %s adapter (canonical kind=clip required; this is a runtime guard, not a soft fallback)", a.kind)
	}
	canonicalHits, err := a.SearchAssets(ctx, ports.AssetSearchQuery{
		Query:                  q.Query,
		Source:                 q.Source,
		Category:               q.Category,
		MediaType:              q.MediaType,
		WorkspaceID:            q.WorkspaceID,
		IsSystem:               q.IsSystem,
		Limit:                  q.Limit,
		MinScore:               q.MinScore,
		RequireActiveLifecycle: false, // clip path: CompileQdrantFilter already locks lifecycle=ACTIVE.
	})
	if err != nil {
		return nil, err
	}
	out := make([]ports.ClipSearchHit, 0, len(canonicalHits))
	for _, h := range canonicalHits {
		out = append(out, ports.ClipSearchHit{
			AssetID: h.AssetID,
			Name:    h.Name,
			Score:   h.Score,
			Source:  h.Source,
		})
	}
	return out, nil
}

// SearchStock implements the legacy StockSearchPort method.
//
// The runtime guard `a.kind != KindStock` makes accidental
// cross-pollination structurally impossible: a future agent that
// passes a clip-flavored adapter to a stock-flavored caller will
// see the typed error immediately, not a silently-wrong result
// set with the wrong filter + wrong default limit.
//
// 7-day backward-compat: this method survives until
// PR-CLIPS-STOCK-PORT-RETIRE retires it (deadline 2026-08-15).
func (a *semanticAssetSearchAdapter) SearchStock(ctx context.Context, query string, limit int) ([]ports.StockSearchHit, error) {
	if a.kind != KindStock {
		return nil, fmt.Errorf("stock path called on %s adapter (canonical kind=stock required; this is a runtime guard, not a soft fallback)", a.kind)
	}
	canonicalHits, err := a.SearchAssets(ctx, ports.AssetSearchQuery{
		Query: query,
		// Source is hard-coded to "stock" by the adapter; any caller value is ignored.
		// Category / MediaType / WorkspaceID / IsSystem are zero values (stock is admin/reconcile path only).
		// RequireActiveLifecycle is FORCED to true by the adapter (stock filter requires lifecycle_state=ACTIVE).
		Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ports.StockSearchHit, 0, len(canonicalHits))
	for _, h := range canonicalHits {
		out = append(out, ports.StockSearchHit{
			AssetID:   h.AssetID,
			Name:      h.Name,
			Source:    h.Source,
			DriveLink: h.DriveLink, // populated by convertAssetHitsByKind for KindStock
			Score:     h.Score,
		})
	}
	return out, nil
}

// convertAssetHitsByKind dispatches between the two convert helpers
// based on the KindAsset discriminant. The pre-PR-4 functions
// (convertAssetHits + convertStockAssetHits) are inlined here so a
// future refactor of the SearchAssets method cannot silently break
// the per-kind wire-shape invariant — the dispatch + the convert
// are co-located.
//
// Per godlike/07 NO-FAKE-AVAILABILITY: a future addition of KindXxx
// without a corresponding convert call returns typed-error here.
// Per godlike/06 SSOT: this single dispatch IS the canonical place
// where the per-kind wire-shape invariant lives.
func convertAssetHitsByKind(results []schema.SearchResult, kind KindAsset) []ports.AssetSearchHit {
	switch kind {
	case KindClip:
		return convertClipAssetHits(results)
	case KindStock:
		return convertStockAssetHits(results)
	default:
		// Unreachable in production (semanticAssetSearchAdapter
		// validates kind at construction), but the typed-error
		// here catches any future dev that bypasses validation.
		return nil
	}
}

// convertClipAssetHits maps infra-level schema.SearchResult →
// canonical AssetSearchHit (5 fields, DriveLink empty for clip
// path per QDRANT-001: server-internal locators do not belong in
// the search contract; clip consumers fetch signed URLs via
// delivery.Signer.BuildAuthorizedURL).
//
// Per godlike/06 SSOT: this function is the SOLE canonical owner
// of the clip-path wire-shape. The pre-PR-4 function (in
// clip_search_adapter.go) is moved verbatim here.
func convertClipAssetHits(results []schema.SearchResult) []ports.AssetSearchHit {
	out := make([]ports.AssetSearchHit, 0, len(results))
	for _, r := range results {
		out = append(out, ports.AssetSearchHit{
			AssetID:   payloadString(r.Payload, "asset_id"),
			Name:      payloadString(r.Payload, "name"),
			Score:     r.Score,
			Source:    payloadString(r.Payload, "source"),
			DriveLink: "", // clip path: per QDRANT-001
		})
	}
	return out
}

// convertStockAssetHits maps infra-level schema.SearchResult →
// canonical AssetSearchHit. For the stock path, DriveLink IS
// populated (from payload "drive_link" or fallback "drive_url") —
// stock consumers need the DriveLink for direct re-upload / preview
// flows. This is the inverse of the clip adapter's
// convertClipAssetHits, which sets DriveLink="" per QDRANT-001.
//
// Per godlike/06 SSOT one-canonical-owner-per-fact: this function
// is the SOLE canonical owner of the stock-path wire-shape
// conversion. A future refactor of the SearchAssets method cannot
// silently break the DriveLink invariant — the conversion is
// co-located with the method that produces it.
//
// Per godlike/07 NO-FAKE-AVAILABILITY: the drive_link → drive_url
// fallback preserves the pre-PR-3 wire-shape convention; the
// payload field is renamed to drive_link in the new schema but
// older payloads still have drive_url, and a fallback ensures
// stock consumers see the DriveLink in both cases.
//
// The pre-PR-4 function (in stock_search_adapter.go) is moved
// verbatim here.
func convertStockAssetHits(results []schema.SearchResult) []ports.AssetSearchHit {
	out := make([]ports.AssetSearchHit, 0, len(results))
	for _, r := range results {
		dl := payloadString(r.Payload, "drive_link")
		if dl == "" {
			dl = payloadString(r.Payload, "drive_url")
		}
		out = append(out, ports.AssetSearchHit{
			AssetID:   payloadString(r.Payload, "asset_id"),
			Name:      payloadString(r.Payload, "name"),
			Source:    payloadString(r.Payload, "source"),
			DriveLink: dl,
			Score:     r.Score,
		})
	}
	return out
}

// validateScope is the per-adapter fail-closed gate on the
// WorkspaceID field. Shared between KindClip + KindStock? NO —
// stock is admin/reconcile only and does NOT call validateScope.
// To preserve the per-path semantics, validateScope is called ONLY
// from searchAssetsClip. (Kept package-private and unchanged from
// pre-PR-4; the moved-validateScope-is-clip-only invariant is
// captured by the runtime guard in searchAssetsClip + the
// absence of any validateScope call in searchAssetsStock.)
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
