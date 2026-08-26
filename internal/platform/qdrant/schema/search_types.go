// search_types.go — Search/scroll wire shapes + verifier ports (PR3 split).
//
// PR3 mechanical split (June 2026): relocated from types.go without
// signature or behaviour changes. The search family covers:
//   - SearchRequest + HybridSearchRequest + SparseQueryVector (client-side
//     request shapes targeting /points/query)
//   - SearchResult (server-side response shape, both ANN-and-hybrid)
//   - ScrollResult + ScrollPoint (server-side scroll-batch shape)
//   - DeadLetterChecker + GoldenQueryRunner (verifier ports used by
//     ReindexVerifier; they cluster with the verifier's "report
//     gates" surface, kept here for call-site locality with the
//     SwitchReport shape they participate in building)
//
// Filter maps inside SearchRequest/HybridSearchRequest stay inline
// `map[string]any` — there is no dedicated Filter/Condition/Match
// type in the codebase, hence filter_types.go is a (separate) doc-only
// marker pointing at the inline shape.
package schema

import ()

import "context"

// ── Search types ─────────────────────────────────────────────────────

// SearchRequest is a canonical ANN search request.
type SearchRequest struct {
	QueryVector []float32      `json:"vector"`
	VectorName  string         `json:"vector_name"`
	Limit       int            `json:"limit"`
	MinScore    float64        `json:"min_score,omitempty"`
	Filter      map[string]any `json:"filter,omitempty"`

	// Convenience filter fields — set directly instead of building a Filter map.
	// If Filter is also set, the combination is implementation-defined.
	Source    string `json:"-"`
	Category  string `json:"-"`
	MediaType string `json:"-"`
	Language  string `json:"-"`
}

// HybridSearchRequest combines dense + sparse for hybrid retrieval.
//
// PR2 (fix/qdrant-bm25-indexing): server-side BM25 inference is
// the canonical strategy. The orchestrator passes the raw query
// text via SparseText (plus SparseModel, defaulting to
// DefaultSparseModel) and Qdrant tokenizes + weights + projects the
// sparse vector ON THE SERVER. The legacy client-side Raw vector
// path (SparseQueryVector) is preserved for diagnostic / bulk-from-csv
// flows that already have a pre-computed sparse representation; live
// retrieval MUST go through SparseText. See pkg/bm25 for the
// deprecation status of the client-side tokenizer.
type HybridSearchRequest struct {
	DenseVector          []float32 `json:"dense_vector"`
	DenseVectorName      string    `json:"dense_vector_name"`
	TranscriptVector     []float32 `json:"transcript_vector,omitempty"`
	TranscriptVectorName string    `json:"transcript_vector_name,omitempty"`
	SparseVectorName     string    `json:"sparse_vector_name,omitempty"`
	// SparseText (preferred, PR2+): raw text that Qdrant tokenizes
	// server-side via the SparseModel. Empty SparseText falls through
	// to the raw SparseQueryVector path (kept for diagnostic / bulk
	// flows only).
	SparseText string `json:"sparse_text,omitempty"`
	// SparseModel is the inference model used to project SparseText
	// into a sparse vector. Empty defaults to DefaultSparseModel.
	SparseModel string `json:"sparse_model,omitempty"`
	// SparseQueryVector carries the legacy client-side BM25
	// tokenization result (only used when SparseText is empty).
	// Kept for diagnostic / bulk-from-csv paths; production
	// orchestrators should set SparseText and let Qdrant handle
	// tokenization against the model configured on the sparse
	// channel.
	SparseQueryVector *SparseQueryVector `json:"sparse_query_vector,omitempty"`
	Limit             int                `json:"limit"`
	MinScore          float64            `json:"min_score,omitempty"`
	Filter            map[string]any     `json:"filter,omitempty"`

	// Convenience filter fields.
	Source    string `json:"-"`
	Category  string `json:"-"`
	MediaType string `json:"-"`
	Language  string `json:"-"`
}

// SparseQueryVector is a Qdrant-compatible sparse vector for hybrid search.
// Indices are hashed token IDs; Values are term-frequency scores in (0, 1].
type SparseQueryVector struct {
	Indices []uint32  `json:"indices"`
	Values  []float32 `json:"values"`
}

// SearchResult is a single match from Qdrant.
// Raw fields (ID, Score, Payload, Version) come directly from the Qdrant API.
// Derived fields (AssetID, Name, …) are populated from Payload by convenience
// helpers (searchResultFromPoint, etc.).
//
// QDRANT-001 (June 2026): LocalPath and DriveLink have been
// removed from this struct AND from appsearch.VectorSearchResult.
// Server-internal locators (filesystem path + Drive web-view link)
// have no place in the canonical search contract — the rule is
// "SearchResult carries IDs + metadata for hydration, never a
// server-internal locator". BuildPayload no longer writes them;
// search_adapter.go no longer reads them. Clients that need bytes
// go through delivery.Signer.BuildAuthorizedURL per asset.
type SearchResult struct {
	// Raw Qdrant fields.
	ID      string         `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload,omitempty"`
	Version int64          `json:"version,omitempty"`

	// Derived convenience fields (populated from Payload).
	AssetID        string   `json:"asset_id,omitempty"`
	QdrantPointID  string   `json:"qdrant_point_id,omitempty"`
	Source         string   `json:"source,omitempty"`
	Name           string   `json:"name,omitempty"`
	Category       string   `json:"category,omitempty"`
	MediaType      string   `json:"media_type,omitempty"`
	Style          string   `json:"style,omitempty"`
	Language       string   `json:"language,omitempty"`
	YouTubeVideoID string   `json:"youtube_video_id,omitempty"`
	YouTubeURL     string   `json:"youtube_url,omitempty"`
	StartTime      string   `json:"start_time,omitempty"`
	EndTime        string   `json:"end_time,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	SearchText     string   `json:"search_text,omitempty"`
	// LocalPath/DriveLink REMOVED (QDRANT-004 cleanup): they were
	// server-internal locators. The application search DTO
	// (appsearch.VectorSearchResult), the asset store, index writer,
	// and stale-link cleaner have all migrated off them. No callers
	// remain of `qdrant.SearchResult.LocalPath` / `.DriveLink`.
}

// ── Scroll types ─────────────────────────────────────────────────────

// ScrollResult holds a page of scrolled points and the next offset.
type ScrollResult struct {
	Points     []ScrollPoint `json:"points"`
	NextOffset string        `json:"next_offset"`
}

// ScrollPoint is a single Qdrant point returned by the scroll API.
type ScrollPoint struct {
	ID      string         `json:"id"`
	Payload map[string]any `json:"payload"`
	// Vector carries the stored vector(s) when the scroll request asks
	// for them (with_vector=true). It is nil/empty for payload-only scrolls.
	Vector map[string]any `json:"vector,omitempty"`
}

// ── Verifier ports (used by ReindexVerifier + SwitchReport gaps) ─────

// DeadLetterChecker is an optional dependency for the reindex verifier.
// Implementations count open dead-letter events from the outbox.
type DeadLetterChecker interface {
	CountOpen(ctx context.Context) (int, error)
}

// GoldenQueryRunner is the port verifier.go uses for the "golden queries" block in the
// SwitchReport. It was previously an empty-marker interface (IsEmptyMarker()) — the marker
// was removed in July 2026 (YAGNI: zero implementations, zero callers). The verifier already
// nil-checks the goldenQueries field; callers can pass nil when no runner is wired.
//
// Future: when a concrete golden-query runner is added (e.g. http-based), define methods
// on this interface and wire them in runGoldenQuerySmoke.
type GoldenQueryRunner interface {
	// (empty — was IsEmptyMarker() before July 2026)
}
