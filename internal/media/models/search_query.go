package models

// SearchQuery represents a recurring YouTube topic search.
// E.g. "Floyd Mayweather interview" → monitor finds new videos periodically.
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

// SearchQueryResult records a processed video from a search query.
// Used for dedup — prevents re-processing the same video.
type SearchQueryResult struct {
	QueryID     string `json:"query_id"`
	VideoID     string `json:"video_id"`
	VideoTitle  string `json:"video_title"`
	ChannelName string `json:"channel_name,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	ProcessedAt string `json:"processed_at"`
	Score       int    `json:"score"`
}

// TableName returns the DB table name.
func (SearchQuery) TableName() string { return "search_queries" }

// TableName returns the DB table name.
func (SearchQueryResult) TableName() string { return "search_query_results" }
