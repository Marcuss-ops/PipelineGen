// Package search is the canonical Search capability (Wave 21 — Fase 4,
// June 2026).
//
// Background. Three legacy subsystems fan out media-asset search today:
//   - internal/application/assets/clipssearch        (advanced, multi-source)
//   - internal/application/assets/search             (cross-provider, ProviderResult)
//   - internal/application/mediasearch               (semantic, vector-backed)
//
// Wave 21 (this PR + PR 9 + PR 10) consolidates the three under a single
// Query/Result/Candidate/Filters contract living in this package. PR 8 ships
// the contract + Go-level type-identity with mediasearch. PR 9 ships the
// real SearchAggregator with fanout, dedup, ranking, cursor. PR 10 cuts over
// the handlers and deletes the legacy packages.
//
// Wave 19 — internal/application/<capA>/ must NOT import
// internal/application/<capB>/ unless via a shared port. search exports
// backend interfaces (SearchBackend, BackendRegistry); concrete adapters
// live under internal/app (composition root only). mediasearch consumes
// the canonical Filters/SearchMode via Go type aliases declared at the
// top of internal/application/mediasearch/types.go — Go aliases are
// bidirectional identity, so the cross-capability reference resolves at
// the type-system level (compile-time) without violating Wave 19.
package search

import "errors"

// ── Capability enum ────────────────────────────────────────────────
//
// Capability advertises which MediaTypes a SearchBackend serves.
// Aggregator.Eligible filters backends by Query.MediaTypes ∩ Backend.Capabilities.
type Capability string

const (
	CapVideo Capability = "video"
	CapImage Capability = "image"
	CapAudio Capability = "audio"
	CapMusic Capability = "music"
)

// ── Mode enum ──────────────────────────────────────────────────────
//
// SearchMode toggles ANN vs. hybrid (semantic backend only).
// Bidirectional alias: mediasearch.SearchMode = search.SearchMode.
type SearchMode string

const (
	SearchModeANN    SearchMode = "ann"
	SearchModeHybrid SearchMode = "hybrid"
)

// ── Filters ─────────────────────────────────────────────────────────
//
// Filters is the unified search filter set.
// Bidirectional alias: mediasearch.MediaSearchFilter = search.Filters.
//
// Field names match legacy MediaSearchFilter 1:1 (Source / MediaType /
// Category / Language / Tags / DurationMsMin) so the byte-equivalence
// test on /internal/v1/media/search survives both PR 8 (alias-only) and
// PR 10 (alias-with-removed-local-def).
type Filters struct {
	Source        string
	MediaType     string
	Category      string
	Language      string
	Tags          []string // AND semantics: every tag must be present
	DurationMsMin int      // inclusive lower bound on duration (videos only)
}

// ── Cursor ──────────────────────────────────────────────────────────
//
// Cursor is an opaque pagination token. Wire format is base64url(JSON
// {"v":1,"items":[{"a","s","src"},…]}). Encoder/Decoder live in cursor.go.
// Empty Cursor = first page.
type Cursor string

// ── Limit bounds ────────────────────────────────────────────────────
//
// Centralised limit constants used internally by the aggregator and
// referenced by SearchAdapter impls. Handler-level defaults stay in
// each handler (legacy handler limits preserved).
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// ── Error sentinels ─────────────────────────────────────────────────

var (
	// ErrInvalidCursor is returned by DecodeCursor for malformed input
	// (bad base64, bad JSON, unknown version marker, etc.). Handlers map
	// this to HTTP 422 (semantic error, not a transient failure).
	ErrInvalidCursor = errors.New("search: invalid cursor")

	// ErrEmptyCandidate is returned by dedupKey when a Candidate carries
	// no identity (no AssetID, no SourceRef, no URL, no Hash). The
	// aggregator drops empty-identity candidates silently per dedup
	// policy; callers see ErrEmptyCandidate only when a builder
	// callback tries to mint an empty cursor from them.
	ErrEmptyCandidate = errors.New("search: empty candidate")
)

// ── Actor ────────────────────────────────────────────────────────────
//
// Actor carries the tenant identity propagated to every backend.
// PR-1 spec: "L'adapter deve inoltrare il contesto reale, senza
// forzare IsAdmin". Wire shape: middleware extracts the Identity
// (JWT, mTLS, session token), authenticates it, and sets the
// corresponding Actor fields before invoking the handler. The
// search package delegates authentication to upstream layers —
// an Actor with WorkspaceID=="" is rejected by the semantic
// backend (QDRANT-004 ErrMissingWorkspace contract) rather than
// silently degraded to admin. Field names are public so JSON
// encoding for cross-service calls stays simple.
type Actor struct {
	WorkspaceID string // tenant workspace; empty disables semantic backend
	UserID      string // optional user-level identifier for audit
	IsAdmin     bool   // admin principals may pick arbitrary workspaces
}

// IsZero reports whether the Actor has no identity fields set.
// Equivalent to Actor{} but cheaper to read in hot paths; used by
// the semantic backend to decide whether to fall back to the
// composition-time default workspace.
func (a Actor) IsZero() bool {
	return a.WorkspaceID == "" && a.UserID == "" && !a.IsAdmin
}

// ── Query ───────────────────────────────────────────────────────────
//
// Query is the canonical orchestrator input. One type for all backends.
// Text is trimmed by SearchAggregator before fanout (callers MAY pass
// already-trimmed text — idempotent). Sources []string empty = "all
// registered backends". MediaTypes []string empty = "all capabilities".
//
// PR-1 (June 2026): Actor carries tenant identity down to every
// backend. Handlers MUST set it from auth middleware; backends MUST
// forward it instead of substituting default values.
type Query struct {
	Text       string   // trimmed before fanout
	Hash       string   // PR-2 (June 2026): when non-empty, hash-match backends answer; text-match backends ignore
	Sources    []string // empty = all from registry; aliases resolved via ResolveCanonicals
	MediaTypes []string // empty = all ("video","image","audio","music")
	Filters    Filters
	Limit      int        // 0 → aggregator defaults to DefaultLimit, capped MaxLimit
	Cursor     string     // opaque base64-JSON; "" = first page
	Mode       SearchMode // applied to the semantic backend only
	Actor      Actor      // PR-1: tenant identity forwarded to every backend
}

// ── Candidate ──────────────────────────────────────────────────────
//
// Candidate is the universal search hit. JSON tags deliberately avoid
// server-internal locators — QDRANT-004 invariant. Only signed delivery
// URLs or external provider references (YouTube VideoID, etc.) survive
// to clients.
//
// Score is normalised [0,1] across backends. Hash is a content hash
// when known (used by dedup policy rank-order 4).
type Candidate struct {
	AssetID      string  `json:"asset_id"`
	Source       string  `json:"source"`               // "youtube","artlist","local","semantic"
	SourceRef    string  `json:"source_ref,omitempty"` // provider-native ID (YouTube VideoID, artlist ID)
	MediaType    string  `json:"media_type,omitempty"`
	Title        string  `json:"title,omitempty"`
	Name         string  `json:"name,omitempty"`       // canonical asset name; may differ from Title when localizations differ
	LocalPath    string  `json:"local_path,omitempty"` // for hash-source + duplicate-list display
	DriveLink    string  `json:"drive_link,omitempty"` // for hash-source + duplicate-list display
	ThumbnailURL string  `json:"thumbnail_url,omitempty"`
	PreviewURL   string  `json:"preview_url,omitempty"` // signed; NEVER raw Drive URL
	Score        float64 `json:"score"`
	Hash         string  `json:"hash,omitempty"`
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
