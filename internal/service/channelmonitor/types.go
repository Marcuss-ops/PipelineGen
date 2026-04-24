// Package channelmonitor monitors YouTube channels for trending videos,
// extracts subtitles, downloads interesting clips, and uploads them to Drive Stock.
package channelmonitor

import (
	"time"
)

// ChannelConfig holds configuration for a monitored channel
type ChannelConfig struct {
	URL             string   `json:"url"`                   // e.g. "https://www.youtube.com/@vladtv"
	Category        string   `json:"category"`              // e.g. "Music", "HipHop", "Wwe"
	Keywords        []string `json:"keywords"`              // keywords to match in titles for relevance
	MinViews        int64    `json:"min_views"`             // minimum views to consider
	MaxClipDuration int      `json:"max_clip_duration"`     // max seconds per clip (default 60)
	MaxVideos       int      `json:"max_videos,omitempty"`  // max videos to process per run (default 5)
	MaxClips        int      `json:"max_clips,omitempty"`   // max clips to extract per video (default 5)
	BaseTimeoutSec  int      `json:"base_timeout_sec"`      // base download timeout seconds (default 90)
	FolderName      string   `json:"folder_name,omitempty"` // optional stable Drive subfolder name
}

// MonitorConfig holds the full monitor configuration
type MonitorConfig struct {
	Channels          []ChannelConfig `json:"channels"`
	CheckInterval     time.Duration   `json:"check_interval"`      // default 24h
	VideoTimeframe    string          `json:"video_timeframe"`     // 24h, week, month (default month)
	ClipRootID        string          `json:"clip_root_id"`        // Drive Clips root folder ID
	ClipRunDBPath     string          `json:"clip_run_db_path"`    // local SQLite store for clip runs
	YtDlpPath         string          `json:"ytdlp_path"`          // path to yt-dlp binary
	FFmpegPath        string          `json:"ffmpeg_path"`         // path to ffmpeg binary
	CookiesPath       string          `json:"cookies_path"`        // optional: browser cookies file
	MaxClipDuration   int             `json:"max_clip_duration"`   // default 60s
	OllamaURL         string          `json:"ollama_url"`          // Ollama server URL
	DefaultMaxClips   int             `json:"default_max_clips"`   // default clips per video
	DefaultTimeoutSec int             `json:"default_timeout_sec"` // base download timeout
}

// ClipResult represents a downloaded and uploaded clip
type ClipResult struct {
	VideoID      string  `json:"video_id"`
	VideoTitle   string  `json:"video_title"`
	ClipFile     string  `json:"clip_file"`
	StartSec     int     `json:"start_sec,omitempty"`
	EndSec       int     `json:"end_sec,omitempty"`
	Duration     int     `json:"duration"`
	Description  string  `json:"description"`
	Confidence   float64 `json:"confidence,omitempty"`
	NeedsReview  bool    `json:"needs_review,omitempty"`
	Status       string  `json:"status,omitempty"`
	DriveFileID  string  `json:"drive_file_id"`
	DriveFileURL string  `json:"drive_file_url"`
	TxtFileID    string  `json:"txt_file_id"`
}

// VideoResult represents the result of processing one video
type VideoResult struct {
	VideoID    string             `json:"video_id"`
	Title      string             `json:"title"`
	Channel    string             `json:"channel"`
	Views      int64              `json:"views"`
	Transcript string             `json:"transcript"`
	Highlights []HighlightSegment `json:"highlights"` // interesting segments with timestamps
	Clips      []ClipResult       `json:"clips"`
	FolderPath string             `json:"folder_path"`
}

// HighlightSegment represents a highlight with its timestamp position in the transcript
type HighlightSegment struct {
	Text     string `json:"text"`
	StartSec int    `json:"start_sec"`
	EndSec   int    `json:"end_sec"`
	Duration int    `json:"duration"`
}

// CategoryDecision captures the classification result used for routing and review.
type CategoryDecision struct {
	Category    string  `json:"category"`
	Reason      string  `json:"reason,omitempty"`
	Source      string  `json:"source,omitempty"` // override, gemma, fallback
	Confidence  float64 `json:"confidence"`
	NeedsReview bool    `json:"needs_review"`
}

// ProcessedVideoEntry tracks a video that has been processed by the monitor
type ProcessedVideoEntry struct {
	VideoID     string    `json:"video_id"`
	Title       string    `json:"title"`
	Channel     string    `json:"channel"`
	ProcessedAt time.Time `json:"processed_at"`
	FolderPath  string    `json:"folder_path"`
	ClipsCount  int       `json:"clips_count"`
}

// knownCategories maps normalized category names to canonical names
var knownCategories = map[string]string{
	"boxing":      "Boxe",
	"boxe":        "Boxe",
	"pugilato":    "Boxe",
	"mma":         "Boxe",
	"ufc":         "Boxe",
	"criminal":    "Crime",
	"crime":       "Crime",
	"crimine":     "Crime",
	"arrest":      "Crime",
	"mafia":       "Crime",
	"documentary": "Discovery",
	"celebrity":   "Discovery",
	"discovery":   "Discovery",
	"science":     "Discovery",
	"hip hop":     "HipHop",
	"hiphop":      "HipHop",
	"rap":         "HipHop",
	"drill":       "HipHop",
	"trap":        "HipHop",
	"musica":      "Music",
	"music":       "Music",
	"singer":      "Music",
	"song":        "Music",
	"artist":      "Music",
	"cantante":    "Music",
	"wrestling":   "Wwe",
	"wwe":         "Wwe",
	"various":     "Various",
	"misc":        "Various",
	"other":       "Various",
}

// ClipRunStatus tracks the lifecycle of a single clip extraction/upload attempt.
type ClipRunStatus string

const (
	ClipRunStatusPending     ClipRunStatus = "pending"
	ClipRunStatusDownloading ClipRunStatus = "downloading"
	ClipRunStatusRendered    ClipRunStatus = "rendered"
	ClipRunStatusUploaded    ClipRunStatus = "uploaded"
	ClipRunStatusNeedsReview ClipRunStatus = "needs_review"
	ClipRunStatusFailed      ClipRunStatus = "failed"
	ClipRunStatusSkipped     ClipRunStatus = "skipped"
)

// ClipRunRecord stores the local clip DB row for idempotence and reconstruction.
type ClipRunRecord struct {
	RunKey       string        `json:"run_key"`
	VideoID      string        `json:"video_id"`
	Title        string        `json:"title"`
	FolderPath   string        `json:"folder_path,omitempty"`
	Category     string        `json:"category,omitempty"`
	Confidence   float64       `json:"confidence"`
	NeedsReview  bool          `json:"needs_review"`
	SegmentIdx   int           `json:"segment_idx"`
	StartSec     int           `json:"start_sec"`
	EndSec       int           `json:"end_sec"`
	Duration     int           `json:"duration"`
	Status       ClipRunStatus `json:"status"`
	FileName     string        `json:"file_name,omitempty"`
	DriveFileID  string        `json:"drive_file_id,omitempty"`
	DriveFileURL string        `json:"drive_file_url,omitempty"`
	TxtFileID    string        `json:"txt_file_id,omitempty"`
	Error        string        `json:"error,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// ClipVideoSummary is the machine-readable video summary stored alongside the text summary.
type ClipVideoSummary struct {
	VideoID     string                 `json:"video_id"`
	Title       string                 `json:"title"`
	FolderPath  string                 `json:"folder_path"`
	Category    string                 `json:"category"`
	Confidence  float64                `json:"confidence"`
	NeedsReview bool                   `json:"needs_review"`
	GeneratedAt time.Time              `json:"generated_at"`
	Clips       []ClipVideoSummaryItem `json:"clips"`
}

// ClipVideoSummaryItem stores the minimal data needed to reconstruct a clip run.
type ClipVideoSummaryItem struct {
	SegmentIdx   int           `json:"segment_idx"`
	StartSec     int           `json:"start_sec"`
	EndSec       int           `json:"end_sec"`
	Duration     int           `json:"duration"`
	Confidence   float64       `json:"confidence"`
	NeedsReview  bool          `json:"needs_review"`
	Status       ClipRunStatus `json:"status"`
	DriveFileID  string        `json:"drive_file_id,omitempty"`
	DriveFileURL string        `json:"drive_file_url,omitempty"`
	TxtFileID    string        `json:"txt_file_id,omitempty"`
	Error        string        `json:"error,omitempty"`
}
