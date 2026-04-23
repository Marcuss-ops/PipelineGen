package harvester

import (
	"context"
	"sync"
	"time"

	"velox/go-master/internal/downloader"
	"velox/go-master/internal/queue"
	"velox/go-master/internal/upload/drive"
)

type Config struct {
	Enabled            bool          `json:"enabled"`
	CheckInterval      time.Duration `json:"check_interval"`
	SearchQueries      []string      `json:"search_queries"`
	Channels           []string      `json:"channels"`
	MaxResultsPerQuery int           `json:"max_results_per_query"`
	MinViews           int64         `json:"min_views"`
	Timeframe          string        `json:"timeframe"`
	MaxConcurrentDls   int           `json:"max_concurrent_downloads"`
	DownloadDir        string        `json:"download_dir"`
	ProcessClips       bool          `json:"process_clips"`
	DriveFolderID      string        `json:"drive_folder_id"`
}

type SearchResult struct {
	VideoID    string    `json:"video_id"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	Views      int64     `json:"views"`
	Duration   int       `json:"duration"`
	Channel    string    `json:"channel"`
	UploadedAt time.Time `json:"uploaded_at"`
	Thumbnail  string    `json:"thumbnail"`
}

type HarvestResult struct {
	VideoID     string `json:"video_id"`
	Title       string `json:"title"`
	Downloaded  bool   `json:"downloaded"`
	Processed   bool   `json:"processed"`
	Uploaded    bool   `json:"uploaded"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	DriveURL    string `json:"drive_url,omitempty"`
	Error       string `json:"error,omitempty"`
}

type BlacklistRecord struct {
	VideoID       string    `json:"video_id"`
	Reason        string    `json:"reason"`
	Score         float64   `json:"score"`
	BlacklistedAt time.Time `json:"blacklisted_at"`
}

type Harvester struct {
	config        *Config
	youtubeClient YouTubeSearcher
	downloader    downloader.Downloader
	driveClient   *drive.Client
	db            ClipDatabase
	queue         queue.Queue
	blacklist     []BlacklistRecord
	downloadCh    chan SearchResult
	resultCh      chan HarvestResult
	wg            sync.WaitGroup
	running       bool
	stopCh        chan struct{}
	stopOnce      sync.Once // prevents double-close panic on stopCh
}

type ClipDatabase interface {
	AddClip(record *ClipRecord) error
	GetClip(videoID string) (*ClipRecord, error)
	ClipExists(videoID string) (bool, error)
	UpdateClip(record *ClipRecord) error
}

type ClipRecord struct {
	VideoID      string    `json:"video_id"`
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	Views        int64     `json:"views"`
	Duration     int       `json:"duration"`
	Channel      string    `json:"channel"`
	Downloaded   bool      `json:"downloaded"`
	DownloadPath string    `json:"download_path"`
	DriveFileID  string    `json:"drive_file_id"`
	DriveURL     string    `json:"drive_url"`
	FolderPath   string    `json:"folder_path"`
	ProcessedAt  time.Time `json:"processed_at"`
	UploadedAt   time.Time `json:"uploaded_at"`
	CreatedAt    time.Time `json:"created_at"`
}

type YouTubeSearcher interface {
	Search(ctx context.Context, query string, opts *SearchOptions) ([]SearchResult, error)
	SearchByChannel(ctx context.Context, channelID string, opts *SearchOptions) ([]SearchResult, error)
	GetVideoStats(ctx context.Context, videoID string) (*SearchResult, error)
}

type SearchOptions struct {
	MaxResults int
	SortBy     string
	Timeframe  string
	ChannelID  string
}
