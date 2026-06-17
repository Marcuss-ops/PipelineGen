package models

// CategoryChannel represents a YouTube channel subscribed to a specific category/folder.
// Each Drive folder category (e.g. "boxe", "rap", "comedy") can have multiple channels.
// When the Channel Monitor runs, it checks these channels and downloads clips
// that match the category.
type CategoryChannel struct {
	ID              string `json:"id"`
	Category        string `json:"category"`
	ChannelURL      string `json:"channel_url"`
	ChannelName     string `json:"channel_name,omitempty"`
	Keywords        string `json:"keywords,omitempty"` // JSON array — title-level keyword match
	MinViews        int    `json:"min_views,omitempty"`
	MaxClipDuration int    `json:"max_clip_duration,omitempty"`
	DriveFolderID   string `json:"drive_folder_id,omitempty"`

	// SemanticKeywords enables transcript-level content matching.
	// JSON array of themes/topics (e.g. '["health","dieta","fitness"]').
	// When set, monitor downloads subtitles and asks Ollama for relevance score.
	SemanticKeywords string `json:"semantic_keywords,omitempty"`

	// MinSemanticScore is the minimum Ollama confidence (0-100) to accept a match.
	// Default 60. Higher = fewer but more relevant clips.
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

// TableName returns the database table name for the CategoryChannel model
func (CategoryChannel) TableName() string {
	return "category_channels"
}
