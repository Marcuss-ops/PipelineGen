// Package stock provides video stock management functionality.
package stock

import (
	"context"
	"time"
)

// Manager defines the stock management contract used by handlers.
type Manager interface {
	ListProjects(ctx context.Context) ([]Project, error)
	CreateProject(ctx context.Context, name string, config *ProjectConfig) (*Project, error)
	GetProject(ctx context.Context, name string) (*Project, error)
	DeleteProject(ctx context.Context, name string) error
	Search(ctx context.Context, query string, sources []string) ([]SearchResult, error)
	SearchYouTube(ctx context.Context, query string, maxResults int) ([]VideoResult, error)
	DownloadVideo(ctx context.Context, url string, projectName string) (*DownloadTask, error)
	GetDownloadStatus(ctx context.Context, taskID string) (*DownloadStatus, error)
	ProcessProject(ctx context.Context, options *ProcessOptions) (*ProcessResult, error)
	GetReport(ctx context.Context, projectName string) (*ProjectReport, error)
}

// Project represents a stock project.
type Project struct {
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	VideoCount  int       `json:"video_count"`
	TotalSize   int64     `json:"total_size_bytes"`
	Status      string    `json:"status"`
	Tags        []string  `json:"tags"`
	Description string    `json:"description"`
}

// ProjectConfig stores project configuration.
type ProjectConfig struct {
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	OutputDir   string   `json:"output_dir"`
}

// SearchResult is a generic search result.
type SearchResult struct {
	Source      string `json:"source"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Duration    int    `json:"duration"`
	Thumbnail   string `json:"thumbnail"`
	Description string `json:"description"`
}

// VideoResult is a YouTube-specific search result.
type VideoResult struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Duration    int    `json:"duration"`
	Thumbnail   string `json:"thumbnail"`
	Description string `json:"description"`
	Uploader    string `json:"uploader"`
	ViewCount   int    `json:"view_count"`
	UploadDate  string `json:"upload_date,omitempty"`
}

// DownloadTask tracks a download in progress.
type DownloadTask struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	ProjectName string    `json:"project_name"`
	Status      string    `json:"status"`
	Progress    int       `json:"progress"`
	OutputPath  string    `json:"output_path,omitempty"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// DownloadStatus exposes current download state.
type DownloadStatus struct {
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	Progress   int    `json:"progress"`
	Speed      string `json:"speed,omitempty"`
	ETA        string `json:"eta,omitempty"`
	OutputPath string `json:"output_path,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ProcessOptions describes project processing inputs.
type ProcessOptions struct {
	ProjectName    string   `json:"project_name"`
	Videos         []string `json:"videos,omitempty"`
	Format         string   `json:"format,omitempty"`
	Quality        string   `json:"quality,omitempty"`
	Resolution     string   `json:"resolution,omitempty"`
	RemoveAudio    bool     `json:"remove_audio,omitempty"`
	GenerateThumbs bool     `json:"generate_thumbnails,omitempty"`
}

// ProcessResult describes processing output.
type ProcessResult struct {
	ProjectName     string   `json:"project_name"`
	VideosProcessed int      `json:"videos_processed"`
	TotalSize       int64    `json:"total_size"`
	OutputFiles     []string `json:"output_files,omitempty"`
	Duration        float64  `json:"processing_time_seconds"`
}

// ProjectReport summarizes project contents.
type ProjectReport struct {
	Project       Project     `json:"project"`
	Videos        []VideoInfo `json:"videos"`
	TotalSize     int64       `json:"total_size"`
	TotalDuration int         `json:"total_duration_seconds"`
	LastUpdated   time.Time   `json:"last_updated"`
}

// VideoInfo describes a local downloaded file.
type VideoInfo struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	Duration   int       `json:"duration,omitempty"`
	Resolution string    `json:"resolution,omitempty"`
	Format     string    `json:"format,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}
