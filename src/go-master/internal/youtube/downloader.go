// Package youtube provides YouTube video download functionality for Agent 5.
package youtube

// Downloader wrapper using new unified client
type Downloader struct {
	client    Client
	outputDir string
	format    string
	quality   string
}

// NewDownloader creates a new downloader using the unified client
func NewDownloader(outputDir string) *Downloader {
	client, err := NewDefaultClient()
	if err != nil {
		// Fallback to yt-dlp backend if default fails
		client = NewYtDlpBackend(nil)
	}
	
	return &Downloader{
		client:    client,
		outputDir: outputDir,
		format:    "best[ext=mp4]/best",
		quality:   "1080",
	}
}

// SetFormat sets the download format
func (d *Downloader) SetFormat(format string) {
	d.format = format
}

// Legacy types for backward compatibility with old downloader_* files
type LegacyVideoInfo struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Duration     float64 `json:"duration"`
	Description  string  `json:"description"`
	Uploader     string  `json:"uploader"`
	ViewCount    int     `json:"view_count"`
	ThumbnailURL string  `json:"thumbnail"`
	URL          string  `json:"webpage_url"`
}

type LegacySearchResult struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	URL           string `json:"url"`
	Channel       string `json:"uploader"`
	ChannelID     string `json:"channel_id"`
	ChannelURL    string `json:"channel_url"`
	ViewCount     int    `json:"view_count"`
	ViewCountFmt  string `json:"view_count_formatted"`
	Duration      string `json:"duration"`
	DurationSec   int    `json:"duration_seconds"`
	UploadDate    string `json:"upload_date"`
	Thumbnail     string `json:"thumbnail"`
}

type LegacySubtitleResult struct {
	YouTubeURL string `json:"youtube_url"`
	Language   string `json:"language"`
	CharCount  int    `json:"char_count"`
	VTTContent string `json:"vtt_content"`
}

type LegacyDetailedVideoInfo struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	URL           string   `json:"url"`
	Thumbnail     string   `json:"thumbnail"`
	ThumbnailHQ   string   `json:"thumbnail_hq"`
	ThumbnailMax  string   `json:"thumbnail_max"`
	Duration      string   `json:"duration"`
	DurationSec   int      `json:"duration_seconds"`
	UploadDate    string   `json:"upload_date"`
	ViewCount     int64    `json:"view_count"`
	LikeCount     string   `json:"like_count"`
	ChannelID     string   `json:"channel_id"`
	Channel       string   `json:"channel"`
	ChannelURL    string   `json:"channel_url"`
	Tags          []string `json:"tags"`
	Categories    []string `json:"categories"`
}

type LegacyChannelAnalytics struct {
	Channel struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		URL         string `json:"url"`
		Subscribers string `json:"subscribers"`
	} `json:"channel"`
	Analytics struct {
		TotalVideos          int    `json:"total_videos"`
		TotalViews           int64  `json:"total_views"`
		TotalViewsFmt        string `json:"total_views_formatted"`
		AverageViews         int    `json:"average_views"`
		AverageViewsFmt      string `json:"average_views_formatted"`
		AverageDurationSec   int    `json:"average_duration_seconds"`
		AverageDuration      string `json:"average_duration"`
	} `json:"analytics"`
	RecentVideos []LegacySearchResult `json:"recent_videos"`
}
