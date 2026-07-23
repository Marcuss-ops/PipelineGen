package asset

// CategoryChannel represents a YouTube channel subscribed to a specific
// category/folder.
type CategoryChannel struct {
	ID                  string `json:"id"`
	Category            string `json:"category"`
	ChannelURL          string `json:"channel_url"`
	ChannelName         string `json:"channel_name,omitempty"`
	Keywords            string `json:"keywords,omitempty"`
	MinViews            int    `json:"min_views,omitempty"`
	MaxClipDuration     int    `json:"max_clip_duration,omitempty"`
	DriveFolderID       string `json:"drive_folder_id,omitempty"`
	SemanticKeywords    string `json:"semantic_keywords,omitempty"`
	MinSemanticScore    int    `json:"min_semantic_score,omitempty"`
	PlaylistEnd         int    `json:"playlist_end,omitempty"`
	CheckInterval       string `json:"check_interval,omitempty"`
	MaxVideosPerRun     int    `json:"max_videos_per_run,omitempty"`
	Priority            int    `json:"priority,omitempty"`
	LookbackDays        int    `json:"lookback_days,omitempty"`
	MaxSegments         int    `json:"max_segments,omitempty"`
	SegmentPrompt       string `json:"segment_prompt,omitempty"`
	Enabled             int    `json:"enabled,omitempty"`
	NextCheckAt         string `json:"next_check_at,omitempty"`
	LastCheckedAt       string `json:"last_checked_at,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	LastSuccessAt       string `json:"last_success_at,omitempty"`
	LeaseOwner          string `json:"lease_owner,omitempty"`
	LeaseUntil          string `json:"lease_until,omitempty"`
	LastCursor          string `json:"last_cursor,omitempty"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

// TableName returns the database table name for the CategoryChannel model.
func (CategoryChannel) TableName() string {
	return "category_channels"
}
