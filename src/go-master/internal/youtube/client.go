// Package youtube provides a unified YouTube client interface
// supporting multiple backend implementations (yt-dlp, native Go libraries, etc.)
package youtube

import (
	"context"
	"io"
	"time"
)

// Client is the main interface for YouTube operations
type Client interface {
	// Video operations
	GetVideo(ctx context.Context, videoID string) (*VideoInfo, error)
	Download(ctx context.Context, req *DownloadRequest) (*DownloadResult, error)
	DownloadAudio(ctx context.Context, req *AudioDownloadRequest) (*AudioDownloadResult, error)
	
	// Search operations
	Search(ctx context.Context, query string, opts *SearchOptions) ([]SearchResult, error)
	GetChannelVideos(ctx context.Context, channelURL string, opts *ChannelOptions) ([]SearchResult, error)
	GetTrending(ctx context.Context, region string, limit int) ([]SearchResult, error)
	
	// Subtitle operations
	GetSubtitles(ctx context.Context, videoID string, lang string) (*SubtitleInfo, error)
	GetTranscript(ctx context.Context, url string, lang string) (string, error)
	
	// Utility
	CheckAvailable(ctx context.Context) error
}

// VideoInfo represents metadata about a YouTube video
type VideoInfo struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	ChannelID     string    `json:"channel_id"`
	Channel       string    `json:"channel"`
	ChannelURL    string    `json:"channel_url"`
	Duration      time.Duration `json:"duration"`
	Views         int64     `json:"view_count"`
	Likes         int64     `json:"like_count"`
	UploadDate    time.Time `json:"upload_date"`
	Thumbnails    []Thumbnail `json:"thumbnails"`
	Tags          []string  `json:"tags,omitempty"`
	Categories    []string  `json:"categories,omitempty"`
}

// Thumbnail represents a video thumbnail at different resolutions
type Thumbnail struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Quality string `json:"quality,omitempty"` // default, medium, high, maxres
}

// Format represents an available video format/stream
type Format struct {
	ID            string `json:"id"`
	Ext           string `json:"ext"`
	Resolution    string `json:"resolution"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	FPS           int    `json:"fps"`
	VCodec        string `json:"vcodec"`
	ACodec        string `json:"acodec"`
	Bitrate       int64  `json:"bitrate"`
	FileSize      int64  `json:"filesize"`
	AudioChannels int    `json:"audio_channels"`
	HasAudio      bool   `json:"has_audio"`
	HasVideo      bool   `json:"has_video"`
	Quality       string `json:"quality"` // descriptive quality (e.g., "1080p", "720p")
}

// FormatList is a list of formats with filtering helpers
type FormatList []Format

// WithAudio returns formats that have audio
func (fl FormatList) WithAudio() FormatList {
	var result FormatList
	for _, f := range fl {
		if f.HasAudio {
			result = append(result, f)
		}
	}
	return result
}

// WithVideo returns formats that have video
func (fl FormatList) WithVideo() FormatList {
	var result FormatList
	for _, f := range fl {
		if f.HasVideo {
			result = append(result, f)
		}
	}
	return result
}

// Quality filters formats by minimum quality string
func (fl FormatList) Quality(minQuality string) FormatList {
	qualityMap := map[string]int{
		"144p": 144, "240p": 240, "360p": 360, "480p": 480,
		"720p": 720, "1080p": 1080, "1440p": 1440, "2160p": 2160,
	}
	
	minHeight, ok := qualityMap[minQuality]
	if !ok {
		return fl
	}
	
	var result FormatList
	for _, f := range fl {
		if f.Height >= minHeight {
			result = append(result, f)
		}
	}
	return result
}

// Best returns the best quality format by bitrate
func (fl FormatList) Best() *Format {
	if len(fl) == 0 {
		return nil
	}
	best := &fl[0]
	for i := range fl {
		if fl[i].Bitrate > best.Bitrate {
			best = &fl[i]
		}
	}
	return best
}

// DownloadRequest contains parameters for downloading a video
type DownloadRequest struct {
	URL         string      `json:"url"`
	VideoID     string      `json:"video_id"` // Extracted from URL if not provided
	OutputDir   string      `json:"output_dir"`
	OutputFile  string      `json:"output_file"` // Filename without extension
	Format      string      `json:"format"` // Format selector (e.g., "best[height<=1080]")
	MaxHeight   int         `json:"max_height"` // Maximum video height (e.g., 1080)
	Quality     string      `json:"quality"` // Preferred quality (e.g., "1080p", "720p")
	CookiesFile string      `json:"cookies_file"` // Path to browser cookies for authentication
	Proxy       string      `json:"proxy"` // Optional proxy URL
	Retries     int         `json:"retries"` // Number of retry attempts (default: 3)
	Progress    ProgressCallback `json:"-"` // Optional progress callback
}

// AudioDownloadRequest contains parameters for downloading audio only
type AudioDownloadRequest struct {
	URL         string `json:"url"`
	VideoID     string `json:"video_id"`
	OutputDir   string `json:"output_dir"`
	OutputFile  string `json:"output_file"`
	AudioFormat string `json:"audio_format"` // mp3, m4a, opus, etc.
	AudioQuality string `json:"audio_quality"` // audio quality selector
	CookiesFile string `json:"cookies_file"`
	Proxy       string `json:"proxy"`
	Retries     int    `json:"retries"`
	Progress    ProgressCallback `json:"-"`
}

// DownloadResult represents the result of a video download
type DownloadResult struct {
	VideoID    string    `json:"video_id"`
	Title      string    `json:"title"`
	FilePath   string    `json:"file_path"`
	FileSize   int64     `json:"file_size"`
	Duration   time.Duration `json:"duration"`
	Thumbnail  string    `json:"thumbnail"`
	Author     string    `json:"author"`
	Format     string    `json:"format"`
	Resolution string    `json:"resolution"`
}

// AudioDownloadResult represents the result of an audio download
type AudioDownloadResult struct {
	VideoID  string `json:"video_id"`
	Title    string `json:"title"`
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
	Duration time.Duration `json:"duration"`
	Format   string `json:"format"` // mp3, m4a, etc.
}

// SearchOptions contains parameters for searching
type SearchOptions struct {
	MaxResults int    `json:"max_results"`
	SortBy     string `json:"sort_by"` // relevance, rating, date, views
	UploadDate string `json:"upload_date"` // hour, today, week, month, year
	Duration   string `json:"duration"` // short, medium, long
}

// SearchResult represents a YouTube search result
type SearchResult struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	URL           string `json:"url"`
	Channel       string `json:"channel"`
	ChannelID     string `json:"channel_id"`
	ChannelURL    string `json:"channel_url"`
	Duration      time.Duration `json:"duration"`
	Views         int64  `json:"view_count"`
	UploadDate    string `json:"upload_date"`
	Thumbnail     string `json:"thumbnail"`
	Description   string `json:"description,omitempty"`
}

// ChannelOptions contains parameters for channel video listing
type ChannelOptions struct {
	Limit      int    `json:"limit"`
	SortBy     string `json:"sort_by"` // newest, oldest, popular
}

// SubtitleInfo represents subtitle data
type SubtitleInfo struct {
	VideoID   string `json:"video_id"`
	Language  string `json:"language"`
	VTTContent string `json:"vtt_content"`
	URL       string `json:"url,omitempty"`
}

// ProgressCallback is called during download to report progress
type ProgressCallback func(percent float64, downloadedBytes int64, totalBytes int64)

// SearchQuery represents a structured search query
type SearchQuery struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
	SearchType string `json:"search_type"` // standard, interviews, highlights
}

// DownloadProgress tracks the progress of an ongoing download
type DownloadProgress struct {
	VideoID       string  `json:"video_id"`
	Percent       float64 `json:"percent"`
	Downloaded    int64   `json:"downloaded_bytes"`
	Total         int64   `json:"total_bytes"`
	Speed         float64 `json:"speed_bytes_per_sec"`
	ETA           int     `json:"eta_seconds"`
	Status        string  `json:"status"` // downloading, completed, failed
	Error         string  `json:"error,omitempty"`
}

// Config holds global configuration for the YouTube client
type Config struct {
	// Backend selection
	Backend string `json:"backend"` // "ytdlp" (default), "native", etc.
	
	// yt-dlp configuration
	YtDlpPath     string `json:"ytdlp_path"` // Path to yt-dlp binary
	FFmpegPath    string `json:"ffmpeg_path"` // Path to ffmpeg binary
	
	// Default download settings
	DefaultFormat    string `json:"default_format"`
	DefaultMaxHeight int    `json:"default_max_height"`
	DefaultRetries   int    `json:"default_retries"`
	
	// Authentication
	DefaultCookiesFile string `json:"default_cookies_file"`
	
	// Network
	Proxy string `json:"proxy"`
	
	// Rate limiting
	MaxConcurrentDownloads int `json:"max_concurrent_downloads"`
	
	// GPU acceleration (for post-processing)
	GPUAcceleration bool   `json:"gpu_acceleration"`
	GPUDevice       int    `json:"gpu_device"` // NVIDIA GPU device index
}

// Stream represents a readable stream from a video format
type Stream struct {
	io.ReadCloser
	ContentLength int64
	MimeType      string
}
