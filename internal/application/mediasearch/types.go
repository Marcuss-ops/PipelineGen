// Package mediasearch — types.go defines the domain DTOs that flow
// through the MediaSearch use case. These types are intentionally
// shaped to never carry a server-internal locator (LocalPath,
// InternalRootURL, FileSystemPath, raw Drive file IDs, etc.) —
// QDRANT-004 acceptance criterion: "Nessun path locale o secret
// esposto".
package mediasearch

// MediaSearchRequest is the canonical orchestrator input. The handler
// translates the JSON DTO into this shape before invoking the
// service. Validation lives at the handler boundary (JSON binding);
// the service assumes a well-formed request.
type MediaSearchRequest struct {
	Query     string            // required, trimmed
	Mode      SearchMode        // default SearchModeHybrid
	Limit     int               // default 10, capped at 50
	MinScore  float64           // default 0 (service falls back to cfg)
	Filters   MediaSearchFilter // optional
	Workspace WorkspaceContext  // required (handler sets it from auth)
}

// MediaSearchFilter mirrors the OData-style filter set the spec
// describes. Empty strings disable the relevant filter; numeric
// DurationMsMin == 0 disables the duration predicate.
type MediaSearchFilter struct {
	Source        string   // "stock", "youtube", "artlist", ...
	MediaType     string   // "video", "image", "audio"
	Category      string   // topology bucket
	Language      string   // BCP-47 code
	Tags          []string // AND semantics: all tags must be present
	DurationMsMin int      // inclusive lower bound on duration (videos only)
}

// MediaSearchResponse is the orchestrator output. The JSON DTO is a
// direct mirror; we keep the wire shape here so callers and the
// service agree without an additional translation pass.
type MediaSearchResponse struct {
	OK        bool           `json:"ok"`
	Query     QueryEcho      `json:"query"`
	Count     int            `json:"count"`
	Hits      []SearchHit    `json:"hits"`
	RequestID string         `json:"request_id,omitempty"`
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
