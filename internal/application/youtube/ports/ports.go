// Package ports — application-layer port interfaces + DTOs for the YouTube pipeline.
//
// Per PR3 (June 2026): extracted from the root youtube package into a dedicated
// ports/ package so the 12 structural ports and canonical DTOs live at a single
// importable location. The youtube orchestrator (service_orchestrator.go) and all
// capability files import this package.
//
// Per PR1.7 (June 2026): structural ports carry signature-bearing method sets so
// compile-time `var _ <Port> = (*<Concrete>)(nil)` assertions catch drift.
// Empty markers are reserved for opaque injection tokens.
//
// Per PR2 (June 2026): the concrete `DownloaderMetadata` DTO replaced the empty-marker
// `YouTubeMetadataPort interface{}` bug in `VideoCutResult.Metadata`.
package ports

import (
	"context"
	"time"

	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Domain DTOs (canonical shape at the application–infra seam) ────────────

// DownloaderMetadata is the canonical app-layer DTO produced by every
// video-metadata fetcher (yt-dlp dump-json, YouTube Data API, etc.).
type DownloaderMetadata struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	URL          string           `json:"url,omitempty"`
	Description  string           `json:"description"`
	Duration     float64          `json:"duration"`
	Uploader     string           `json:"uploader"`
	UploadDate   string           `json:"upload_date"`
	ViewCount    int64            `json:"view_count"`
	Language     string           `json:"language"`
	ThumbnailURL string           `json:"thumbnail"`
	Thumbnails   []VideoThumbnail `json:"thumbnails,omitempty"`
	Chapters     []VideoChapter   `json:"chapters,omitempty"`
	Categories   []string         `json:"categories,omitempty"`
	Tags         []string         `json:"tags,omitempty"`
	CachedAt     time.Time        `json:"cached_at,omitempty"`
}

// VideoChapter represents a single chapter slice in a video.
type VideoChapter struct {
	Title     string  `json:"title"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
}

// VideoThumbnail represents a single thumbnail variant in a video.
type VideoThumbnail struct {
	URL    string `json:"url"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// UploadResultDTO mirrors the relevant fields of infra/drive.UploadResult.
type UploadResultDTO struct {
	FileID      string `json:"file_id"`
	WebViewLink string `json:"web_view_link,omitempty"`
}

// SearchLiveResult is the per-video result row returned by YouTube search.
type SearchLiveResult struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	URL       string  `json:"url"`
	Uploader  string  `json:"uploader"`
	Duration  float64 `json:"duration"`
	Thumbnail string  `json:"thumbnail"`
}

// VideoMetadata is a back-compat alias for DownloaderMetadata.
type VideoMetadata = DownloaderMetadata

// YouTubeMetadataPort is a back-compat alias for DownloaderMetadata.
type YouTubeMetadataPort = DownloaderMetadata

// ── Structural ports (signature-bearing) ─────────────────────────────────

type ClipStorePort interface {
	DB() *sql.DB
	Get(ctx context.Context, id string) (*asset.Asset, error)
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
	GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error)
	Upsert(ctx context.Context, m *asset.Asset) error
	UpsertFolder(ctx context.Context, f *asset.ClipFolder) error
	DeleteClip(ctx context.Context, id string) error
}

type MonitorsStorePort interface {
	UpsertSource(ctx context.Context, source *asset.MonitoredSource) error
	IncrementProcessed(ctx context.Context, id string) error
}

type VideoMetadataFetcherPort interface {
	GetVideoMetadata(ctx context.Context, videoURL string) (*DownloaderMetadata, error)
}

type DriveFolderManagerPort interface {
	GetOrCreateFolder(ctx context.Context, channelName, parentFolderID string) (string, error)
	UploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*UploadResultDTO, bool, error)
}

type FolderMemoryPort interface {
	LoadManifest(manifestPath string) (*asset.ClipManifest, error)
	SaveManifest(manifestPath string, manifest *asset.ClipManifest) error
	UpdateManifestTXT(folder *asset.ClipFolder, manifest *asset.ClipManifest) error
	ComputeManifestStats(manifest *asset.ClipManifest) asset.ClipFolderStats
}

type OllamaClientPort interface {
	SimpleGenerate(ctx context.Context, model, prompt string, timeout time.Duration, opts map[string]any) (string, error)
}

type SearchRunnerPort interface {
	SearchLive(ctx context.Context, query string, limit int, sort string) ([]SearchLiveResult, error)
	GetVideoInfo(ctx context.Context, videoURL string) (*DownloaderMetadata, error)
}

type ClipIndexerPort interface {
	IsEnabled() bool
	IndexClip(ctx context.Context, id string) error
}

type SubtitleFetcherPort interface {
	SliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error
}

type WhisperTranscriberPort interface {
	TranscribeAudio(ctx context.Context, audioPath string) (string, error)
}

type ClipFilesPort interface {
	UsableCachedClip(localPath string) (bool, error)
}

type HashServicePort interface {
	MD5String(data string) string
	MD5File(path string) (string, error)
}

type TempFileManagerPort interface{}

type YouTubeCacheStorePort interface{}
