package media

import "time"

// Subject represents a known entity (person, place, thing) for image generation.
type Subject struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	WikidataID  string    `json:"wikidata_id,omitempty"`
	Aliases     []string  `json:"aliases"`
	Category    string    `json:"category"`
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ImageAsset represents a generated or sourced image.
type ImageAsset struct {
	ID           int64     `json:"id"`
	Hash         string    `json:"hash"`
	SubjectID    int64     `json:"subject_id"`
	SlugID       string    `json:"slug_id,omitempty"`
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

// ImageUsage tracks where an image has been used.
type ImageUsage struct {
	ID      int64     `json:"id"`
	ImageID int64     `json:"image_id"`
	VideoID string    `json:"video_id"`
	UsedAt  time.Time `json:"used_at"`
}

// ImageTag is a tag for image categorization.
type ImageTag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// GenerationStyle defines a named style for image/video generation.
type GenerationStyle struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
}

// GenerationStyles is a collection of generation styles.
type GenerationStyles struct {
	Styles []GenerationStyle `yaml:"styles" json:"styles"`
}

// CategoryChannel maps a category to a YouTube channel for monitoring.
type CategoryChannel struct {
	ID               string `json:"id"`
	Category         string `json:"category"`
	ChannelURL       string `json:"channel_url"`
	ChannelName      string `json:"channel_name,omitempty"`
	Keywords         string `json:"keywords,omitempty"`
	MinViews         int    `json:"min_views,omitempty"`
	MaxClipDuration  int    `json:"max_clip_duration,omitempty"`
	DriveFolderID    string `json:"drive_folder_id,omitempty"`
	SemanticKeywords string `json:"semantic_keywords,omitempty"`
	MinSemanticScore int    `json:"min_semantic_score,omitempty"`
	PlaylistEnd      int    `json:"playlist_end,omitempty"`
	CheckInterval    string `json:"check_interval,omitempty"`
	MaxVideosPerRun  int    `json:"max_videos_per_run,omitempty"`
	Priority         int    `json:"priority,omitempty"`
	LookbackDays     int    `json:"lookback_days,omitempty"`
	MaxSegments      int    `json:"max_segments,omitempty"`
	SegmentPrompt    string `json:"segment_prompt,omitempty"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

// SearchQuery represents a saved search query for automated monitoring.
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

func (SearchQuery) TableName() string { return "search_queries" }

// SearchQueryResult is a result from a search query execution.
type SearchQueryResult struct {
	QueryID     string `json:"query_id"`
	VideoID     string `json:"video_id"`
	VideoTitle  string `json:"video_title"`
	ChannelName string `json:"channel_name,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	ProcessedAt string `json:"processed_at"`
	Score       int    `json:"score"`
}

func (SearchQueryResult) TableName() string { return "search_query_results" }
