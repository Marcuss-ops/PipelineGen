package mediasearch

// searchRequest is the JSON body. workspace_id and other auth-context fields
// are deliberately absent: tenant scope comes from authenticated context.
type searchRequest struct {
	Query    string              `json:"query" binding:"required"`
	Mode     string              `json:"mode,omitempty"`
	Universe string              `json:"universe,omitempty"`
	Limit    int                 `json:"limit,omitempty"`
	Filters  searchRequestFilter `json:"filters,omitempty"`
}

type searchRequestFilter struct {
	Source        string   `json:"source,omitempty"`
	MediaType     string   `json:"media_type,omitempty"`
	Category      string   `json:"category,omitempty"`
	Language      string   `json:"language,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	DurationMsMin int      `json:"duration_ms_min,omitempty"`
}

// searchResponse is derived from search.Result. Raw Drive locators never cross
// this boundary; only signed delivery URLs are public.
type searchResponse struct {
	OK            bool               `json:"ok"`
	Query         string             `json:"query"`
	Mode          string             `json:"mode"`
	Universe      string             `json:"universe"`
	Count         int                `json:"count"`
	Items         []searchResultItem `json:"items"`
	Partial       bool               `json:"partial,omitempty"`
	Degraded      bool               `json:"degraded,omitempty"`
	BackendErrors map[string]string  `json:"backend_errors,omitempty"`
	ChannelsUsed  []string           `json:"channels_used,omitempty"`
	NextCursor    string             `json:"next_cursor,omitempty"`
	IndexVersion  string             `json:"index_version,omitempty"`
}

type searchResultItem struct {
	AssetID    string  `json:"asset_id"`
	Score      float64 `json:"score"`
	Title      string  `json:"title"`
	Source     string  `json:"source"`
	MediaType  string  `json:"media_type"`
	PreviewURL string  `json:"preview_url"`
}

// ReadinessReport exposes only canonical semantic-media dependencies. Qdrant
// reachability and SQLite hydration were removed by POSTGRES-MEDIA-CUTOVER;
// media_postgres_ready now covers the SSOT that owns pgvector retrieval and
// media_assets hydration.
type ReadinessReport struct {
	Ready              bool   `json:"ready"`
	Embedder           bool   `json:"embedder"`
	SemanticBackend    bool   `json:"semantic_backend"`
	MediaPostgresReady bool   `json:"media_postgres_ready"`
	WorkspaceEnforced  bool   `json:"workspace_enforced"`
	Timestamp          string `json:"timestamp"`
	IndexVersion       string `json:"index_version,omitempty"`
	Failures           string `json:"failures,omitempty"`
}
