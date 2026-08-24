// Package search — types_result.go holds the canonical response-side
// types for the search capability (PR-SEARCH-PORTS-SPLIT, 2026-07-04).
//
// Pre-split, these types lived in types.go alongside the request-side
// types (Query + Actor + Filters). The split separates them by concern.
//
// What lives here (response-side): Candidate + Result.
// What lives in types_query.go: request-side types.
// What moved to errors.go: ErrInvalidCursor + ErrEmptyCandidate
// (godlike/06 SSOT — every sentinel in one place).
package assets

// ── Candidate ──────────────────────────────────────────────────────
//
// Candidate is the universal search hit. Raw server-internal locators
// never cross the public search response boundary — QDRANT-004 invariant.
// DriveLink may be populated from SQLite for internal ranking/operator
// paths, but it is deliberately excluded from JSON responses. Only the
// signed PreviewURL or external provider references survive to clients.
//
// Score is normalised [0,1] across backends. Hash is a content hash
// when known (used by dedup policy rank-order 4).
type Candidate struct {
	AssetID      string   `json:"asset_id"`
	Source       string   `json:"source"`               // "youtube","artlist","local","semantic"
	SourceRef    string   `json:"source_ref,omitempty"` // provider-native ID (YouTube VideoID, artlist ID)
	MediaType    string   `json:"media_type,omitempty"`
	Title        string   `json:"title,omitempty"`
	Name         string   `json:"name,omitempty"`       // canonical asset name; may differ from Title when localizations differ
	SourceURL    string   `json:"source_url,omitempty"` // provider page, never a temporary download URL
	ThumbnailURL string   `json:"thumbnail_url,omitempty"`
	PreviewURL   string   `json:"preview_url,omitempty"` // signed; NEVER raw Drive URL
	DurationMs   int64    `json:"duration_ms,omitempty"`
	Width        int      `json:"width,omitempty"`
	Height       int      `json:"height,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	DriveLink    string   `json:"-"` // internal SQLite enrichment; never public JSON
	Score        float64  `json:"score"`
	Hash         string   `json:"hash,omitempty"`
}

// ── Result ──────────────────────────────────────────────────────────
//
// Result is the canonical typed output. NO `map[string]ProviderResult`
// shape, per project rule "niente map[string]ProviderResult come
// risposta finale primaria" (PR 8 spec).
type Result struct {
	Items          []Candidate
	NextCursor     string            // "" = end of stream
	ProviderErrors map[string]string // backend name → error string (e.g. "youtube" → "rate-limited")
	ChannelsUsed   []string          // which search channels were used (e.g. ["text", "bm25"]); empty when unknown
	Partial        bool              // true if any backend errored
}
