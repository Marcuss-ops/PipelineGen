package detail

// SearchQuery represents a recurring YouTube topic search.
type SearchQuery struct {
	ID                   string `json:"id"`
	Query                string `json:"query"`
	Category             string `json:"category"`
	DriveFolderID        string `json:"drive_folder_id,omitempty"`
	MinScore             int    `json:"min_score"`
	MaxResults           int    `json:"max_results"`
	CheckInterval        string `json:"check_interval"`
	LastRunAt            string `json:"last_run_at,omitempty"`
	LastVideoPublishedAt string `json:"last_video_published_at,omitempty"`
	IsActive             int    `json:"is_active"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
}

// TableName returns the database table name for SearchQuery.
func (SearchQuery) TableName() string { return "search_queries" }

// SearchQueryResult records a processed video from a search query.
type SearchQueryResult struct {
	QueryID     string `json:"query_id"`
	VideoID     string `json:"video_id"`
	VideoTitle  string `json:"video_title"`
	ChannelName string `json:"channel_name,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	ProcessedAt string `json:"processed_at"`
	Score       int    `json:"score"`
}

// TableName returns the database table name for SearchQueryResult.
func (SearchQueryResult) TableName() string { return "search_query_results" }
