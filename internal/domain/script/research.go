package script

// ResearchWebSource is a bounded, sanitized web source retained as provenance.
type ResearchWebSource struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Publisher   string `json:"publisher,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	Excerpt     string `json:"excerpt,omitempty"`
}

type ResearchClaim struct {
	Text      string   `json:"text"`
	SourceIDs []string `json:"source_ids"`
	Verified  bool     `json:"verified"`
}

type ResearchReport struct {
	Status          string              `json:"status"`
	Mode            string              `json:"mode,omitempty"`
	SearchEnabled   bool                `json:"search_enabled"`
	Searched        bool                `json:"searched"`
	CacheSaved      bool                `json:"cache_saved"`
	CacheKey        string              `json:"cache_key,omitempty"`
	ResearchVersion string              `json:"research_version,omitempty"`
	Queries         []string            `json:"queries,omitempty"`
	Sources         []ResearchWebSource `json:"sources,omitempty"`
	Claims          []ResearchClaim     `json:"claims,omitempty"`
	PagesRequested  int                 `json:"pages_requested"`
	PagesFetched    int                 `json:"pages_fetched"`
	PagesFailed     int                 `json:"pages_failed"`
	CacheHit        bool                `json:"cache_hit"`
}
