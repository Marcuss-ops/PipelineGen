// Package scripts — AssetSearchPort is the canonical semantic asset
// discovery surface that subsumes the legacy ClipSearchPort (curate
// path) and StockSearchPort (visual_planning postprocessor) per
// PR-POSTPROCESSOR-UNIFICATION-PHASE-3 (July 2026, deadline 2026-07-22).
//
// godlike/06 SSOT (one canonical owner per fact): AssetSearchPort is
// the SOLE owner of the unified "search assets" interface. A single
// SearchAssets call subsumes the legacy SearchClips (curate path
// with workspace isolation) and SearchStock (visual_planning
// postprocessor with hard-coded source=stock + lifecycle_state=ACTIVE
// filter) operations.
//
// Migration discipline (godlike/07 minimum-blast-radius):
//   - This file is PURELY ADDITIVE: it does not modify the legacy
//     ClipSearchPort or StockSearchPort interfaces (those live in
//     clip_search_port.go + stock_search_port.go and are kept
//     unchanged during the 7-day soak).
//   - In PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 4 (interface
//     link), ClipSearchPort + StockSearchPort will be redefined as
//     interfaces that EMBED AssetSearchPort (Go-idiomatic soft
//     migration: callers see both the new SearchAssets method and
//     the legacy SearchClips/SearchStock methods).
//   - After 7 days of zero regression on the adapter surface,
//     forward-pointer PR-CLIPS-STOCK-PORT-RETIRE will delete the
//     legacy methods from the interfaces and the adapters, then
//     flip ClipSearchPort + StockSearchPort to true Go type
//     aliases (type X = AssetSearchPort) for canonical surface
//     deduplication.
//
// Production adapters (Qdrant side, internal/platform/qdrant/search/):
//   - *ClipSearchAdapter satisfies AssetSearchPort (PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 2)
//   - *StockSearchAdapter satisfies AssetSearchPort (PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 3)
//
// Fail-closed tenant contract (mirrors the legacy ClipSearchPort
// semantics; PR 5 June 2026, fix/qdrant-tenant-scope):
//   - WorkspaceID="" && IsSystem=false → typed error
//     ("workspace required or IsSystem=true explicit").
//   - WorkspaceID="default" → typed error (reserved sentinel).
//   - Otherwise → CompileQdrantFilter emits the canonical
//     workspace + lifecycle filter and Search runs against the
//     runtime alias.
//
// Stock-specific semantics (Source="stock" path):
//   - The adapter applies the hard-coded source=stock +
//     lifecycle_state=ACTIVE filter clause.
//   - When RequireActiveLifecycle==true the adapter applies the
//     lifecycle_state=ACTIVE must-clause (stock default; clip path
//     passes false because CompileQdrantFilter already includes it).
//
// nil-safe in MediaCurator + VisualPlanningProcessor — Qdrant-disabled
// deployments keep working with HintClipIDs-only / nil-port as before;
// the port call is skipped when SetClipSearchPort / SetStockSearchPort
// has never been invoked.
package ports

import "context"

// AssetSearchPort is the canonical semantic asset discovery surface.
// A single SearchAssets call subsumes the legacy SearchClips (curate
// path with workspace isolation) and SearchStock (visual_planning
// postprocessor with hard-coded source=stock + lifecycle_state=ACTIVE
// filter) operations.
type AssetSearchPort interface {
	// SearchAssets performs a single semantic asset search over the
	// configured vector store and returns up to `limit` hits ranked
	// by similarity (highest first). Optional fields (Source,
	// Category, MediaType) are applied as Qdrant filter must-clauses
	// by the adapter. Empty fields = no filter on that axis.
	//
	// minScore defaults to 0.5 (clip) or 0.3 (stock) when zero,
	// matching the legacy per-adapter thresholds.
	//
	// An empty Query returns an empty hit slice with nil error.
	// Adapter errors propagate to the worker, which surfaces them
	// via the standard job-failure contract.
	SearchAssets(ctx context.Context, q AssetSearchQuery) ([]AssetSearchHit, error)
}

// AssetSearchQuery is the unified input contract for
// AssetSearchPort.SearchAssets. It subsumes the legacy
// ClipSearchQuery (8 fields) and the implicit StockSearchPort
// fields (source=stock, lifecycle_state=ACTIVE, MinScore=0.3,
// Limit=5).
type AssetSearchQuery struct {
	// Query is the search text (will be embedded by the adapter).
	Query string
	// Source restricts to a single source family (artlist|youtube|stock).
	// Empty = any. The stock path uses Source="stock" (legacy
	// behaviour); the clip path uses Source="" (no source filter,
	// lets the runtime filter resolve per Qdrant payload).
	Source string
	// Category restricts to a category. Empty = any.
	Category string
	// MediaType restricts to image|video. Empty = any.
	MediaType string
	// WorkspaceID is REQUIRED for user-facing traffic. The adapter
	// applies it as a server-side workspace isolation clause (PR 5,
	// June 2026, fix/qdrant-tenant-scope). An empty workspace means
	// "background/system path" — the adapter then REJECTS the call
	// unless IsSystem=true is set explicitly. The matching rule
	// mirrors mediasearch.Service::Search's ErrMissingWorkspace
	// rejection: a workspace derived from the auth middleware is the
	// contract, an empty workspace is a programming error.
	WorkspaceID string
	// IsSystem opts out of the workspace isolation clause. Only
	// admin / reconcile / DR paths set this. Callers MUST NOT infer
	// it from the request body — the handler should pass it through
	// from an authenticated "is_admin" principal check.
	IsSystem bool
	// Limit is the max number of hits. Zero = adapter default
	// (20 for clip, 5 for stock — matches the legacy per-adapter
	// behaviour).
	Limit int
	// MinScore is the cosine-similarity threshold. Zero = adapter
	// default (0.5 for clip, 0.3 for stock — matches the legacy
	// per-adapter behaviour).
	MinScore float64
	// RequireActiveLifecycle forces the lifecycle_state=ACTIVE
	// must-clause in the adapter's filter. The stock path always
	// sets this to true (its legacy filter hard-codes ACTIVE); the
	// clip path uses the value CompileQdrantFilter emits (already
	// ACTIVE-locked) and typically passes false.
	RequireActiveLifecycle bool

	// FolderNormalizedGroup, when non-empty, emits the canonical
	// Qdrant `normalized_group` must-clause via CompileQdrantFilter.
	// Empty = no folder filter (search across all groups).
	//
	// godlike/06 SSOT: this field is the unwrapped form of the
	// user-facing ports.ClipSearchQuery.Folder / SlotsSearchOptions.Folder
	// (both of which carry *clipfolder.ClipFolderRef). The unwrap
	// happens in the adapter one step up so AssetSearchQuery does
	// NOT depend on the clipfolder package — AssetSearchQuery is
	// the unified surface shared with the stock + image paths which
	// have no folder concept.
	//
	// Wire invariant: the filter uses `normalized_group` (NOT
	// `folder`, `macro_topic`, or `blueprint`). The naming pins
	// the JSON-keyed payload contract across the embedding +
	// projection path; renaming requires a coordinated migration
	// (forward-pointer PR-NORMALIZED-GROUP-KEY).
	FolderNormalizedGroup string

	// ExcludeRightsStatuses + ExcludeReviewStatuses
	// (PR-CLIPINGEST-PIPELINE Step 10, July 2026). When
	// non-empty, the SlotSearchPort adapter (via the underlying
	// Qdrant filter compiler) translates these into a single
	// `MustNot(MatchAny(...))` clause on the corresponding
	// payload fields (`rights_status` + `review_status`).
	//
	// godlike/06 SSOT (one canonical owner per fact): the
	// STRING VALUES populate from
	// `asset.RightsStatus.String()` (6-value alphabet) +
	// `asset.ReviewStatus.String()` (4-value alphabet). A
	// custom caller bypassing the canonical enums MUST use
	// the wire alphabet verbatim (see RightsStatus.Valid /
	// ReviewStatus.Valid in internal/kernel/asset/rights_state.go).
	//
	// godlike/07 fail-closed at the planning-tier: a
	// non-empty ExcludeRightsStatuses slice on a Qdrant
	// collection that has NOT been reindexed with the
	// `rights_status` payload index is a silent filter
	// no-op (MustNot on a missing field matches all rows).
	// The slot_search adapter logs loudly + continues —
	// a future follow-up PR adds the typed error path
	// (ErrRightsFilterRequiresReindex). The SAFE default
	// is a future task; this cycle ships the surface only.
	ExcludeRightsStatuses []string
	ExcludeReviewStatuses []string
}

// AssetSearchHit is the unified result item. It subsumes the legacy
// ClipSearchHit (4 fields) + StockSearchHit (5 fields including
// DriveLink). The DriveLink field is optional via omitempty for the
// clip path per QDRANT-001 (June 2026): server-internal locators do
// not belong in the search contract; clip consumers that need a
// signed URL for an asset should go through the delivery service
// (delivery.Signer.BuildAuthorizedURL).
type AssetSearchHit struct {
	// AssetID is the canonical media_assets.id reference.
	AssetID string `json:"asset_id"`
	// Name is the asset's human-readable name (log/debug only).
	Name string `json:"name"`
	// Score is the cosine similarity from the Qdrant search.
	Score float64 `json:"score"`
	// Source is the source family (artlist|youtube|stock).
	Source string `json:"source"`
	// DriveLink is the Google Drive WebViewLink. Stock path
	// populates this from the Qdrant payload's "drive_link" (or
	// legacy "drive_url") field. Clip path leaves it empty —
	// consumers fetch via delivery.Signer.BuildAuthorizedURL per
	// QDRANT-001.
	DriveLink string `json:"drive_link,omitempty"`
}
