package asset

import "time"

// ── Subjects ────────────────────────────────────────────────────────

// Subject represents a known entity (person, place, thing) for image generation.
type Subject struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	WikidataID  string    `json:"wikidata_id,omitempty"`
	Aliases     []string  `json:"aliases"` // Stored as JSON in the DB.
	Category    string    `json:"category"`
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ── Image Assets ────────────────────────────────────────────────────

// ImageAsset represents an image stored in the asset index.
//
// SubjectID is a string (TEXT in the database) holding the Subject's slug.
// SlugID is an explicit alias used by some callers and stays equivalent to
// SubjectID in practice; preserve both for backward compat.
type ImageAsset struct {
	ID           int64     `json:"id"`
	Hash         string    `json:"hash"`
	SubjectID    string    `json:"subject_id"`        // TEXT in database (slug)
	SlugID       string    `json:"slug_id,omitempty"` // Alias for internal logic
	PathRel      string    `json:"path_rel"`
	SourceURL    string    `json:"source_url"`
	License      string    `json:"license"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	SizeBytes    int64     `json:"size_bytes"`
	QualityScore int       `json:"quality_score"`
	Description  string    `json:"description"`
	DriveFileID  string    `json:"drive_file_id,omitempty"`
	Status       string    `json:"status,omitempty"`
	Error        string    `json:"error,omitempty"`
	MetadataJSON string    `json:"metadata_json"`
	CreatedAt    time.Time `json:"created_at"`
	Tags         []string  `json:"tags,omitempty"`
}

// ImageUsage tracks usage of an image inside a rendered video.
type ImageUsage struct {
	ID      int64     `json:"id"`
	ImageID int64     `json:"image_id"`
	VideoID string    `json:"video_id"`
	UsedAt  time.Time `json:"used_at"`
}

// ImageTag represents a tag associated with an image.
type ImageTag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// ── Categories ──────────────────────────────────────────────────────

// CategoryChannel represents a YouTube channel subscribed to a specific
// category/folder. Each Drive folder category (e.g. "boxe", "rap",
// "comedy") can have multiple channels; when the channel monitor runs it
// checks these channels and downloads clips that match the category.
type CategoryChannel struct {
	ID              string `json:"id"`
	Category        string `json:"category"`
	ChannelURL      string `json:"channel_url"`
	ChannelName     string `json:"channel_name,omitempty"`
	Keywords        string `json:"keywords,omitempty"` // JSON array — title-level keyword match.
	MinViews        int    `json:"min_views,omitempty"`
	MaxClipDuration int    `json:"max_clip_duration,omitempty"`
	DriveFolderID   string `json:"drive_folder_id,omitempty"`

	// SemanticKeywords enables transcript-level content matching.
	// JSON array of themes/topics (e.g. '["health","dieta","fitness"]').
	// When set, monitor downloads subtitles and asks Ollama for relevance score.
	SemanticKeywords string `json:"semantic_keywords,omitempty"`

	// MinSemanticScore is the minimum Ollama confidence (0-100) to accept
	// a match. Default 60. Higher = fewer but more relevant clips.
	MinSemanticScore int `json:"min_semantic_score,omitempty"`

	// PlaylistEnd overrides the global playlist_end for retroactive full-scan.
	// 0 = all videos, -1 = use global default, >0 = specific count.
	PlaylistEnd int `json:"playlist_end,omitempty"`

	// CheckInterval overrides the global check interval for this channel.
	// Format: "7d", "24h", "30m". Default "7d".
	CheckInterval string `json:"check_interval,omitempty"`

	// MaxVideosPerRun limits how many videos are processed per check. 0 = no limit.
	MaxVideosPerRun int `json:"max_videos_per_run,omitempty"`

	// Priority: 1=hot (check 2x), 2=normal, 3=cold (check 0.5x). Default 2.
	Priority int `json:"priority,omitempty"`

	// LookbackDays limits the scan to videos published within N days. 0 = no limit.
	LookbackDays int `json:"lookback_days,omitempty"`

	// MaxSegments limits how many segments to extract per video. Default 2.
	MaxSegments int `json:"max_segments,omitempty"`

	// SegmentPrompt is a custom prompt for the AI segment finder.
	SegmentPrompt string `json:"segment_prompt,omitempty"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// TableName returns the database table name for the CategoryChannel model.
func (CategoryChannel) TableName() string {
	return "category_channels"
}

// ── Search Queries ──────────────────────────────────────────────────

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

// TableName returns the database table name for SearchQuery.
func (SearchQuery) TableName() string { return "search_queries" }

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

// TableName returns the database table name for SearchQueryResult.
func (SearchQueryResult) TableName() string { return "search_query_results" }
