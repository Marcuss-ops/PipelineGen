// Package scripts — ClipSearchPort is the narrow port for semantic
// clip discovery consumed by MediaCurator.
//
// PJ-CURATE-1 (June 2026): productionizes the audit-recommended
// "clip search optional" path. The previous MediaCurator only
// consumed req.HintClipIDs (caller-seeded) and silently fell back
// to text-only if none were supplied. This port lets the worker
// invoke Qdrant on demand (opt-in via Search=true in CurateRequest)
// application/scripts package to Qdrant-specific payload types.
//
// Port shape is intentionally narrow (assetID + score + name) so
// the application layer never sees the full SearchResult.
//
// Production adapter: *qdrant.ClipSearchAdapter (implements via
// Searcher.SearchByText + Searcher.Search with filter must-clauses).
//
// nil-safe in MediaCurator — Qdrant-disabled deployments keep
// working with HintClipIDs-only as before; the port call is
// skipped when SetClipSearchPort has never been invoked.
package ports

import "context"

// ClipSearchPort is the legacy narrow port for semantic clip
// discovery consumed by MediaCurator.
//
// PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 2 (July 2026):
// ClipSearchPort EMBEDS the canonical AssetSearchPort AND retains
// the legacy SearchClips method. This is the Go-idiomatic
// "soft migration" pattern: callers see both the new SearchAssets
// (from the embedded interface) and the legacy SearchClips methods
// during the 7-day soak (FASE-2.1-VOICE-FREEZE discipline).
//
// After 7 days, forward-pointer PR-CLIPS-STOCK-PORT-RETIRE will
// remove the legacy SearchClips method and convert this to:
//
//	type ClipSearchPort = AssetSearchPort
//
// (true Go type alias for canonical surface deduplication).
type ClipSearchPort interface {
	AssetSearchPort

	// SearchClips embeds the query text, performs an ANN search
	// over the configured vector store, and returns up to `limit`
	// clip hits ranked by similarity (highest first).
	//
	// Optional fields (Source, Category, MediaType) are applied as
	// Qdrant filter must-clauses by the adapter. Empty fields =
	// no filter on that axis.
	//
	// minScore defaults to 0.5 if zero (matches the legacy
	// MediaCurator.Curate threshold).
	//
	// An empty Query returns an empty hit slice with nil error.
	// Adapter errors propagate to the worker, which surfaces them
	// via the standard job-failure contract.
	SearchClips(ctx context.Context, q ClipSearchQuery) ([]ClipSearchHit, error)
}

// ClipSearchQuery is the input contract for ClipSearchPort.SearchClips.
type ClipSearchQuery struct {
	// Query is the search text (will be embedded by the adapter).
	Query string
	// Source restricts to a single source family (artlist|youtube|stock).
	// Empty = any.
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
	// Limit is the max number of hits. Zero = 20 (legacy default).
	Limit int
	// MinScore is the cosine-similarity threshold. Zero = 0.5.
	MinScore float64
}

// ClipSearchHit is the result item. AssetID is the canonical
// media_assets.id reference (the only field MediaCurator consumes);
// Score is cosine similarity; Name is for log/debug only.
type ClipSearchHit struct {
	AssetID string
	Name    string
	Score   float64
	Source  string
}
