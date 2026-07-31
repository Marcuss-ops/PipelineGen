package mediasearch

// searchRequest is the JSON body. Note: workspace_id, asset_id, and
// any other auth-context fields are deliberately absent from this
// struct — they MUST come from the auth context, never the body
// (AGENTS.md Hard Rule: never trust client-supplied workspace).
//
// QDRANT-004 §advanced filters: filters.Source/MediaType/Category/
// Language map 1:1 to Qdrant must-predicates (already wired in the
// service). Tags is AND-semantics; DurationMsMin is enforced
// post-hydration (canonical duration comes from SQLite, not the
// vector payload — that's why it's not a vector-store filter).
type searchRequest struct {
	Query   string              `json:"query" binding:"required"`
	Mode    string              `json:"mode,omitempty"` // "ann" or "hybrid"
	Limit   int                 `json:"limit,omitempty"`
	Filters searchRequestFilter `json:"filters,omitempty"`
}

type searchRequestFilter struct {
	Source        string   `json:"source,omitempty"`
	MediaType     string   `json:"media_type,omitempty"`
	Category      string   `json:"category,omitempty"`
	Language      string   `json:"language,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	DurationMsMin int      `json:"duration_ms_min,omitempty"`
}

// searchResponse is the response DTO derived from
// search.Result. Public items intentionally omit raw Drive locators;
// they may exist on the internal Candidate for SQLite hydration but
// never cross this HTTP boundary. OK flips to false only
// when the result is partial AND has zero items (no fake availability).
// Degraded is true when the result is partial but has at least one
// hit (the search "worked" but some backends are down).
//
// BACKFILL/CUTOVER (Commit 2): `BackendErrors` is the SANITIZED
// per-backend-failure map. The keys are backend names (canonical
// SearchBackend.Name()). The values are public-safe failure
// summaries — stack traces, internal URLs, secrets, and
// server-internal locators are STRIPPED by SanitizeProviderErrors
// below. godlike/07 fail-closed: a result partial with empty
// BackendErrors is impossible (the aggregator always populates
// ProviderErrors whenever Partial=true).
//
// IndexVersion is now OMITTABLE — Commit 2 removed the hardcoded
// empty IndexVersion as "index version unknown" (per godlike/07
// no-fake-availability).
type searchResponse struct {
	OK            bool               `json:"ok"`
	Query         string             `json:"query"`
	Mode          string             `json:"mode"`
	Count         int                `json:"count"`
	Items         []searchResultItem `json:"items"`
	Partial       bool               `json:"partial,omitempty"`
	Degraded      bool               `json:"degraded,omitempty"`
	BackendErrors map[string]string  `json:"backend_errors,omitempty"` // SANITIZED — see SanitizeProviderErrors
	ChannelsUsed  []string           `json:"channels_used,omitempty"`
	NextCursor    string             `json:"next_cursor,omitempty"`
	IndexVersion  string             `json:"index_version,omitempty"`
}

// searchResultItem is the public per-result item. Raw Drive locators
// are deliberately absent: only signed delivery URLs may be returned
// to search clients.
type searchResultItem struct {
	AssetID    string  `json:"asset_id"`
	Score      float64 `json:"score"`
	Title      string  `json:"title"`
	Source     string  `json:"source"`
	MediaType  string  `json:"media_type"`
	PreviewURL string  `json:"preview_url"`
}

// ReadinessReport is the JSON DTO for the semantic_search_real probe.
// Each sub-check has its own bool so dashboards surface per-subsystem
// status; the top-level "ready" is fail-closed (Ready AND every
// sub-check is true).
type ReadinessReport struct {
	Ready                bool   `json:"ready"`
	Embedder             bool   `json:"embedder"`
	SemanticBackend      bool   `json:"semantic_backend"`
	QdrantReachable      bool   `json:"qdrant_reachable"`
	SQLiteHydrationReady bool   `json:"sqlite_hydration_ready"`
	WorkspaceEnforced    bool   `json:"workspace_enforced"`
	Timestamp            string `json:"timestamp"`
	IndexVersion         string `json:"index_version,omitempty"`
	Failures             string `json:"failures,omitempty"` // space-joined, sanitized summary
}
