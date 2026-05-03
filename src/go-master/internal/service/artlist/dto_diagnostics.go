package artlist

import "time"

// Stats represents the statistics for Artlist endpoints
type Stats struct {
	OK                 bool    `json:"ok"`
	ClipsTotal         int     `json:"clips_total"`
	ArtlistClipsTotal  int     `json:"artlist_clips_total"`
	SearchTermsTotal   int     `json:"search_terms_total"`
	SearchTermsScraped int     `json:"search_terms_scraped"`
	SearchTermsPending int     `json:"search_terms_pending"`
	CoveragePct        float64 `json:"coverage_pct"`
	LastSyncAt         *string `json:"last_sync_at,omitempty"`
	StaleTerms         int     `json:"stale_terms"`
}

// DiagnosticsResponse reports the current Artlist wiring and database readiness.
type DiagnosticsResponse struct {
	OK                bool    `json:"ok"`
	RootFolderID      string  `json:"root_folder_id,omitempty"`
	DriveFolderID     string  `json:"drive_folder_id,omitempty"`
	NodeScraperDir    string  `json:"node_scraper_dir,omitempty"`
	HasDriveClient    bool    `json:"has_drive_client"`
	HasArtlistDB      bool    `json:"has_artlist_db"`
	MainDBReady       bool    `json:"main_db_ready"`
	ClipsTotal        int     `json:"clips_total"`
	ArtlistClipsTotal int     `json:"artlist_clips_total"`
	SearchTerm        string  `json:"search_term,omitempty"`
	MatchingClips     int     `json:"matching_clips,omitempty"`
	EstimatedSize     int     `json:"estimated_size,omitempty"`
	LastProcessedAt   *string `json:"last_processed_at,omitempty"`
	Error             string  `json:"error,omitempty"`
}

// TermInfo represents information about a search term
type TermInfo struct {
	Term        string    `json:"term"`
	Scraped     bool      `json:"scraped"`
	LastScraped *string   `json:"last_scraped"`
	VideoCount  int       `json:"video_count"`
	CreatedAt   time.Time `json:"created_at"`
}
