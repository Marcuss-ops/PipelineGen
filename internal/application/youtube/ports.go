package youtube

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ────────────────────────────────────────────────────────────────────────
// Ports (interfaces) consumed by the application layer.
//
// Concrete adapters live in internal/infrastructure/youtube and are
// injected via the composition root (internal/app/composition.go).
// The application layer MUST NOT import os/exec, database/sql,
// google.golang.org/api, or any infrastructure package directly.
// ────────────────────────────────────────────────────────────────────────

// VideoInfo is the application-layer DTO for YouTube video metadata.
// Stripped to the fields the application layer actually reads.
type VideoInfoPort struct {
	ID           string
	URL          string
	Title        string
	Description  string
	Uploader     string
	UploadDate   string
	ViewCount    int64
	Duration     float64
	ThumbnailURL string
	Thumbnails   []VideoThumbnailPort
	Chapters     []VideoChapterPort
	Categories   []string
	Tags         []string
}

type VideoThumbnailPort struct {
	URL    string
	Width  int
	Height int
}

type VideoChapterPort struct {
	Title     string
	StartTime float64
	EndTime   float64
}

// LiveSearchResultPort is the raw shape of one yt-dlp search hit.
type LiveSearchResultPort struct {
	ID        string
	URL       string
	Title     string
	Duration  float64
	Uploader  string
	Thumbnail string
}

// SearchRunnerPort runs yt-dlp search/info CLI calls.
// Distinct from the generic downloader so future provider substitution
// (e.g. invidious or piped.video) can keep the same contract.
type SearchRunnerPort interface {
	SearchLive(ctx context.Context, query string, limit int, sort string) ([]LiveSearchResultPort, error)
	GetVideoInfo(ctx context.Context, videoURL string) (VideoInfoPort, error)
}

// SubtitleFetcherPort downloads and parses subtitle tracks for a YouTube video.
type SubtitleFetcherPort interface {
	FetchFullVTT(ctx context.Context, videoURL string) ([]TimedEntryPort, error)
	SliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error
	DownloadSubtitles(ctx context.Context, videoURL string, langs string, outputDir string) (string, error)
}

// TimedEntryPort represents a parsed subtitle cue (seconds).
type TimedEntryPort struct {
	Start float64
	End   float64
	Text  string
}

// WhisperTranscriberPort is the local-transcription fallback when no
// official VTT subtitles are available.
type WhisperTranscriberPort interface {
	TranscribeAudio(ctx context.Context, localPath string) (string, error)
}

// ClipFilesPort is the on-disk media-file manager.
type ClipFilesPort interface {
	WriteMetadataFile(metaPath string, data []byte) error
	WriteTranscriptFile(transcriptPath string, data []byte) error
	RemoveIfStale(localPath string) error
	MD5File(path string) (string, error)
	UsableCachedClip(path string) (bool, error)
}

// VideoMetadataFetcherPort fetches YouTube video metadata for enrichment
// without downloading the video. Used by enrichment.go to get metadata
// when it's not available from the cut result.
type VideoMetadataFetcherPort interface {
	GetVideoMetadata(ctx context.Context, videoURL string) (*YouTubeMetadataPort, error)
}

// YouTubeMetadataPort is the application-layer DTO for YouTube metadata
// used in enrichment and metadata persistence.
type YouTubeMetadataPort struct {
	ID          string
	Title       string
	Description string
	Language    string
	Uploader    string
	UploadDate  string
	ViewCount   int64
	Duration    float64
	ThumbnailURL string
	Categories  []string
	Tags        []string
	Chapters    []VideoChapterPort
}

// DriveFolderManagerPort handles Google Drive folder creation and
// file uploads. Abstracts the Drive SDK so the application layer
// never imports google.golang.org/api.
type DriveFolderManagerPort interface {
	GetOrCreateFolder(ctx context.Context, name, parentFolderID string) (string, error)
	UploadFile(ctx context.Context, filePath, folderID, filename string) (*DriveUploadResult, error)
	UploadFileIfChanged(ctx context.Context, filePath, folderID, filename string) (*DriveUploadResult, bool, error)
}

// DriveUploadResult is the application-layer DTO for a Drive upload.
type DriveUploadResult struct {
	FileID      string
	WebViewLink string
}

// TempFileManagerPort manages temporary files and directories.
type TempFileManagerPort interface {
	CreateTempDir(prefix string) (string, error)
	RemoveAll(path string) error
	WriteFile(path string, data []byte) error
	ReadFile(path string) ([]byte, error)
	ReadDir(path string) ([]string, error)
	MkdirAll(path string) error
	Stat(path string) (FileInfoPort, error)
	Remove(path string) error
}

// FileInfoPort is a minimal file info DTO.
type FileInfoPort struct {
	Size int64
}

// ProcessRunnerPort executes external processes (yt-dlp, ffmpeg, etc.)
// and returns stdout, stderr, and error.
type ProcessRunnerPort interface {
	Run(ctx context.Context, name string, args []string) (stdout, stderr string, err error)
}

// HashServicePort provides file hashing and random string generation.
type HashServicePort interface {
	MD5File(path string) (string, error)
	MD5String(s string) string
	RandomString(n int) string
}

// ────────────────────────────────────────────────────────────────────────
// Remaining ports for PR1.5 — remove concrete infrastructure imports.
// ────────────────────────────────────────────────────────────────────────

// ClipStorePort abstracts the clip CRUD operations previously accessed
// through the concrete *assets.ClipsRepository.
type ClipStorePort interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
	Upsert(ctx context.Context, clip *asset.Asset) error
	DeleteClip(ctx context.Context, id string) error
	UpdateSearchTerms(ctx context.Context, id, source, title string, tags []string, searchText string) error
	GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error)
	ListYouTubeClipIDs(ctx context.Context, limit, offset int) ([]string, error)
	ListEnrichedYouTubeClipIDs(ctx context.Context, limit, offset int) ([]string, error)
}

// MonitorsStorePort abstracts the monitored-source operations previously
// accessed through *assets.MonitorsRepository.
type MonitorsStorePort interface {
	UpsertSource(ctx context.Context, ms *asset.MonitoredSource) error
	IncrementProcessed(ctx context.Context, id string) error
}

// YouTubeCacheStorePort abstracts the raw SQL cache queries against
// youtube_search_cache, youtube_video_metadata_cache, youtube_segments_cache,
// and youtube_category_cache tables. These were previously done via
// clipsRepo.DB() which exposed the raw *sql.DB handle.
type YouTubeCacheStorePort interface {
	GetSearchCache(ctx context.Context, cacheKey string) (resultsJSON string, err error)
	UpsertSearchCache(ctx context.Context, cacheKey string, resultsJSON string) error
	GetMetadataCache(ctx context.Context, videoID string) (metadataJSON string, err error)
	UpsertMetadataCache(ctx context.Context, videoID string, metadataJSON string) error
	IncrementMetadataHits(ctx context.Context, videoID string) error
	ListHotMetadata(ctx context.Context, limit int) ([]YouTubeCacheEntry, error)
	PurgeStaleMetadata(ctx context.Context, staleIDs []string) (int64, error)
	ListAllVideoIDs(ctx context.Context) ([]string, error)
	GetSegmentsCache(ctx context.Context, videoID string) (segmentsJSON string, err error)
	UpsertSegmentsCache(ctx context.Context, videoID string, segmentsJSON string) error
	GetCategoryCache(ctx context.Context, videoTitle string) (category string, err error)
	UpsertCategoryCache(ctx context.Context, videoTitle string, category string) error
}

// YouTubeCacheEntry is a minimal cache row for L1 pre-warming.
type YouTubeCacheEntry struct {
	VideoID     string
	MetadataJSON string
}

// ClipIndexerPort abstracts the clip indexer service previously imported
// from internal/media/clipindexer.
type ClipIndexerPort interface {
	IsEnabled() bool
	IndexClip(ctx context.Context, id string) error
}

// FolderMemoryPort abstracts the folder memory service previously imported
// from internal/media/foldermemory.
type FolderMemoryPort interface {
	LoadManifest(path string) (*asset.ClipManifest, error)
	SaveManifest(path string, manifest *asset.ClipManifest) error
	UpdateManifestTXT(clipFolder *asset.ClipFolder, manifest *asset.ClipManifest) error
	ComputeManifestStats(manifest *asset.ClipManifest) asset.ClipFolderStats
	UpsertFolder(ctx context.Context, clipFolder *asset.ClipFolder) error
}

// OllamaClientPort abstracts the Ollama LLM client previously imported
// from internal/infrastructure/ai/ollama/client.
type OllamaClientPort interface {
	SimpleGenerate(ctx context.Context, model, prompt string, timeout time.Duration, extra map[string]any) (string, error)
}

// DispatcherPort abstracts the outbox dispatcher previously imported
// from internal/infrastructure/database/sqlite/outbox.
type DispatcherPort interface {
	EnqueueAndIndex(ctx context.Context, clip *asset.Asset, hash string) error
}

// DriveClientPort abstracts the Google Drive SDK client previously imported
// from google.golang.org/api/drive/v3. Used only as fallback in
// GetOrCreateChannelFolder when DriveFolderManagerPort is not wired.
type DriveClientPort interface {
	GetOrCreateChannelFolder(ctx context.Context, channelName, parentFolderID string) (string, error)
}

// CategoryClassifierPort abstracts video title classification previously
// imported from internal/media/classifier.
type CategoryClassifierPort interface {
	Classify(ctx context.Context, title string) string
}
