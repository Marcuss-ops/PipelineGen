// Package channelmonitor monitors YouTube channels for trending videos,
// extracts subtitles, downloads interesting clips, and uploads them to Drive Stock.
package channelmonitor

import (
	"time"
)

// ChannelConfig holds configuration for a monitored channel
type ChannelConfig struct {
	URL             string   `json:"url"`               // e.g. "https://www.youtube.com/@vladtv"
	Category        string   `json:"category"`           // e.g. "Music", "HipHop", "Wwe"
	Keywords        []string `json:"keywords"`           // keywords to match in titles for relevance
	MinViews        int64    `json:"min_views"`          // minimum views to consider
	MaxClipDuration int      `json:"max_clip_duration"`  // max seconds per clip (default 60)
}

// MonitorConfig holds the full monitor configuration
type MonitorConfig struct {
	Channels        []ChannelConfig `json:"channels"`
	CheckInterval   time.Duration   `json:"check_interval"`    // default 24h
	StockRootID     string          `json:"stock_root_id"`     // Drive Stock root folder ID
	YtDlpPath       string          `json:"ytdlp_path"`        // path to yt-dlp binary
	CookiesPath     string          `json:"cookies_path"`      // optional: browser cookies file
	MaxClipDuration int             `json:"max_clip_duration"` // default 60s
	OllamaURL       string          `json:"ollama_url"`        // Ollama server URL
}

// ClipResult represents a downloaded and uploaded clip
type ClipResult struct {
	VideoID      string `json:"video_id"`
	VideoTitle   string `json:"video_title"`
	ClipFile     string `json:"clip_file"`
	Duration     int    `json:"duration"`
	Description  string `json:"description"`
	DriveFileID  string `json:"drive_file_id"`
	DriveFileURL string `json:"drive_file_url"`
	TxtFileID    string `json:"txt_file_id"`
}

// VideoResult represents the result of processing one video
type VideoResult struct {
	VideoID    string            `json:"video_id"`
	Title      string            `json:"title"`
	Channel    string            `json:"channel"`
	Views      int64             `json:"views"`
	Transcript string            `json:"transcript"`
	Highlights []HighlightSegment `json:"highlights"` // interesting segments with timestamps
	Clips      []ClipResult      `json:"clips"`
	FolderPath string            `json:"folder_path"`
}

// HighlightSegment represents a highlight with its timestamp position in the transcript
type HighlightSegment struct {
	Text     string `json:"text"`
	StartSec int    `json:"start_sec"`
	EndSec   int    `json:"end_sec"`
	Duration int    `json:"duration"`
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

// CategoryFolderMap maps category names to Drive folder IDs
// These are default values — override via MonitorConfig.ClipsCategoryFolders
var CategoryFolderMap = map[string]string{
	"Boxe":      "1AGJyoOC8tXP8oplh3X2Jrf_0beNkSQzI",
	"Crime":     "1Nq4xcUiloGv3OrAW0DAf5JBIe7Yi08aC",
	"Discovery": "147RID7wyhWqbr7XtfWavIk0T-ozN2YAA",
	"HipHop":    "1ayEZ-CV18xfHQT7RLB4Xgh-TrlkGs-0X",
	"Music":     "16DiW79eGCXO5mgP1dqhE5ZKZCxRovBkq",
	"Wwe":       "1SJo06XvAN0uNf5sP88lhEgE0qJMi0up",
}

// knownCategories maps normalized category names to canonical names
var knownCategories = map[string]string{
	"boxing":      "Boxe",
	"boxe":        "Boxe",
	"pugilato":    "Boxe",
	"criminal":    "Crime",
	"crime":       "Crime",
	"crimine":     "Crime",
	"documentary": "Discovery",
	"celebrity":   "Discovery",
	"discovery":   "Discovery",
	"hip hop":     "HipHop",
	"hiphop":      "HipHop",
	"rap":         "HipHop",
	"drill":       "HipHop",
	"trap":        "HipHop",
	"musica":      "Music",
	"music":       "Music",
	"singer":      "Music",
	"cantante":    "Music",
	"wrestling":   "Wwe",
	"wwe":         "Wwe",
}
