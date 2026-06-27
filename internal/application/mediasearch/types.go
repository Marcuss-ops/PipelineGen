// Package mediasearch — types.go defines the domain DTOs that flow
// through the MediaSearch use case. These types are intentionally
// shaped to never carry a server-internal locator (LocalPath,
// InternalRootURL, FileSystemPath, raw Drive file IDs, etc.) —
// QDRANT-004 acceptance criterion: "Nessun path locale o secret
// esposto".
//
// Wave 21 (Fase 4, June 2026, PR 8) notes
// ─────────────────────────────────────────────────────────────────────
// MediaSearchFilter and SearchMode are now Go-level aliases of the
// canonical contracts in internal/application/search. The new search
// package is the SSOT for the Search capability; mediasearch keeps
// these names for legacy callers (handler.go, service.go, ports.go)
// without copying field shapes. Existing code that constructs or
// consumes MediaSearchFilter / SearchMode keeps compiling because
// aliases are bidirectional identity at the type-system level —
// no "shape-compatible" reconciliation at runtime.
//
// Wave 19 cross-capability reference: this file imports
// internal/application/search (a capability). The reference is valid
// under the "shared port via type identity" reading of Wave 19's
// rule; an entry in docs/migrations/cross-capability-imports-allowlist.txt
// will be registered at the next Wave 19 PR2 promotion cycle to make
// this explicit. PR 10 (CUTOVER) does NOT remove this import — the
// aliases remain so legacy /internal/v1/media/search callers stay
// byte-compatible forever (or until the route is deprecated via a
// separate EXPAND→BACKFILL→CUTOVER migration).
package mediasearch

import (
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// ── Cross-capability aliases (W21 PR 8) ─────────────────────────────
//
// MediaSearchFilter is now a Go-level alias of search.Filters. Field
// shapes match 1:1 so existing service.go literals (req.Filters.Source,
// req.Filters.Tags, etc.) compile unchanged.
type MediaSearchFilter = search.Filters

// MediaSearchRequest is the canonical orchestrator input. The handler
// translates the JSON DTO into this shape before invoking the
// service. Validation lives at the handler boundary (JSON binding);
// the service assumes a well-formed request.
type MediaSearchRequest struct {
	Query     string            // required, trimmed
	Mode      search.SearchMode // default SearchModeHybrid; alias of mediasearch.SearchMode
	Limit     int               // default 10, capped at 50
	MinScore  float64           // default 0 (service falls back to cfg)
	Filters   MediaSearchFilter // optional
	Workspace WorkspaceContext  // required (handler sets it from auth)
}

// MediaSearchResponse is the orchestrator output. The JSON DTO is a
// direct mirror; we keep the wire shape here so callers and the
// service agree without an additional translation pass.
type MediaSearchResponse struct {
	OK           bool        `json:"ok"`
	Query        QueryEcho   `json:"query"`
	Count        int         `json:"count"`
	Hits         []SearchHit `json:"hits"`
	RequestID    string      `json:"request_id,omitempty"`
	IndexVersion string      `json:"index_version,omitempty"`
}

// QueryEcho echoes back the normalised query and the channels used
// for retrieval. Helps debugging and gives clients a contract for
// "what got searched without me having to know about Qdrant".
type QueryEcho struct {
	Normalized   string   `json:"normalized"`
	ChannelsUsed []string `json:"channels_used"`
	Mode         string   `json:"mode"`
}

// SearchHit is one hydrated result.
//
// CRITICAL: The JSON tags here NEVER include local_path or any raw
// locator. The signed DeliveryURL is the only authorised accessor;
// clients calling local_path will get a compile error from this
// struct directly and a nil value from the JSON layer.
type SearchHit struct {
	AssetID         string   `json:"asset_id"`
	Score           float64  `json:"score"`
	MatchedChannels []string `json:"matched_channels,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Name            string   `json:"name,omitempty"`
	Source          string   `json:"source,omitempty"`
	MediaType       string   `json:"media_type,omitempty"`
	Category        string   `json:"category,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Language        string   `json:"language,omitempty"`
	DurationMs      int      `json:"duration_ms,omitempty"`
	Width           int      `json:"width,omitempty"`
	Height          int      `json:"height,omitempty"`
	DeliveryURL     string   `json:"delivery_url,omitempty"`
}

// Limit bounds for the single private API (QDRANT-004 spec).
const (
	DefaultLimit = 10
	MaxLimit     = 50
	DefaultScore = 0.50 // floor below which a hit is dropped pre-hydration
)

// SearchMode is the Wave 21 canonical enum; mediasearch.SearchMode
// is also a Go-level alias for cross-package code that hasn't yet
// been migrated to `search.` references directly. Constants
// SearchModeANN and SearchModeHybrid remain in ports.go so the
// reverse-dependency (port constants, type used in queries) keeps
// functioning — Go allows constants of aliased types.
type SearchMode = search.SearchMode
