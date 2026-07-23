// Package search — types_query.go holds the canonical request-side
// types for the search capability (PR-SEARCH-PORTS-SPLIT, 2026-07-04).
//
// Pre-split, these types lived in types.go alongside the response-side
// types (Candidate + Result). The split separates them by concern so
// the canonical "what callers send" surface is co-located and the
// canonical "what callers receive" surface lives in types_result.go.
//
// What lives here (request-side):
//   - Capability enum (advertises which MediaTypes a SearchBackend serves)
//   - SearchMode enum (ANN vs. hybrid toggle)
//   - Filters (unified search filter set)
//   - Cursor (opaque pagination token; wire format in cursor.go)
//   - DefaultLimit + MaxLimit (centralised limit bounds)
//   - Actor (tenant identity; promoted to canonical via Commit 2)
//   - Query (canonical orchestrator input)
//
// What lives in types_result.go (response-side): Candidate + Result.
//
// What moved to errors.go: ErrInvalidCursor + ErrEmptyCandidate
// (godlike/06 SSOT — every sentinel in one place).
package search

import "strings"

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

// ParseMode maps a wire mode string to a typed SearchMode. Empty or
// unknown values default to SearchModeHybrid. This keeps mode mapping
// in the canonical search package so transport code does not hardcode
// SearchModeANN.
func ParseMode(s string) SearchMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(SearchModeANN):
		return SearchModeANN
	case string(SearchModeHybrid):
		return SearchModeHybrid
	default:
		return SearchModeHybrid
	}
}

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
	IsSystem    bool   // explicit cross-workspace/system scope for admin/reconcile paths
}

// IsZero reports whether the Actor has no identity fields set.
// Equivalent to Actor{} but cheaper to read in hot paths; used by
// the semantic backend to decide whether to fall back to the
// composition-time default workspace.
func (a Actor) IsZero() bool {
	return a.WorkspaceID == "" && a.UserID == "" && !a.IsAdmin && !a.IsSystem
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
	Text           string   // trimmed before fanout
	Hash           string   // PR-2 (June 2026): when non-empty, hash-match backends answer; text-match backends ignore
	Sources        []string // empty = all from registry; aliases resolved via ResolveCanonicals
	MediaTypes     []string // empty = all ("video","image","audio","music")
	Filters        Filters
	Limit          int        // 0 → aggregator defaults to DefaultLimit, capped MaxLimit
	Cursor         string     // opaque base64-JSON; "" = first page
	Mode           SearchMode // applied to the semantic backend only
	Actor          Actor      // PR-1: tenant identity forwarded to every backend
	MinScore       float64    // 0 → use backend default (semanticMinScore 0.50); >0 overrides the score floor
	AllowExternal  bool       // advisory hint: when false, external provider backends should be skipped
	CacheRead      bool       // advisory hint for backends that serve cached content
	PreferApproved bool       // advisory hint for backends that track approval state
}
