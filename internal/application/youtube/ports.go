// Package youtube — application-layer port interfaces + DTOs (PR1.7 cascade June 2026).
//
// Per PR1.7 (June 2026): the setter cascade on the YouTube Service is
// collapsed into a single NewService(ServiceDeps) constructor. The deps
// struct references the port interfaces declared below; wiring is done
// wholesale at composition time instead of one SetXxx call per field.
//
// Per the PR1.7 followup (June 2026): structural ports have signature-bearing
// method sets so the compile-time `var _ <Port> = (*<Concrete>)(nil)`
// assertions catch signature drift. Empty markers are reserved for ports
// whose concrete collaborators are passed as opaque injection tokens.
//
// Per the PR2 followup (June 2026): the previously-empty `YouTubeMetadataPort`
// interface{} was a wrong abstraction — it was a pointer-to-empty-interface
// in `VideoCutResult.Metadata`, which made downstream helpers like
// `ym.Description` fail to compile. Replaced by the concrete
// `DownloaderMetadata` DTO (defined here) plus back-compat aliases
// `VideoMetadata` (kept) and `YouTubeMetadataPort` (kept). The adapter in
// `internal/infrastructure/youtube/` converts `videomuscles.YouTubeMetadata`
// → `*DownloaderMetadata` at the application seam.
//
// Per the PR3 followup (June 2026): `ClipStorePort.UpsertFolder` was added
// so the canonical `*assets.ClipsRepository` can satisfy a single structural
// interface that covers both clip-row AND clip-folder-table operations.
package youtube

import (
	"context"
	"time"

	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Domain DTOs (canonical shape at the application–infra seam) ────────────

// DownloaderMetadata is the canonical app-layer DTO produced by every
// video-metadata fetcher (yt-dlp dump-json, YouTube Data API, etc.).
//
// Fields are exactly the ones the application Service + metadata_persist.go
// helpers actually read; raw yt-dlp JSON parsing belongs in the
// infrastructure layer, never in this package.
//
// Replaces the previous empty-marker `YouTubeMetadataPort interface{}` (June
// 2026 cleanup) and the pointer-to-empty-interface bug in
// `VideoCutResult.Metadata`.
type DownloaderMetadata struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	URL          string         `json:"url,omitempty"`
	Description  string         `json:"description"`
	Duration     float64        `json:"duration"`
	Uploader     string         `json:"uploader"`
	UploadDate   string         `json:"upload_date"`
	ViewCount    int64          `json:"view_count"`
	Language     string         `json:"language"`
	ThumbnailURL string         `json:"thumbnail"`
	Thumbnails   []VideoThumbnail `json:"thumbnails,omitempty"`
	Chapters     []VideoChapter `json:"chapters,omitempty"`
	Categories   []string       `json:"categories,omitempty"`
	Tags         []string       `json:"tags,omitempty"`

	// CachedAt is populated by infra adapters that read this DTO back from
	// the youtube_video_metadata_cache table; otherwise it is the zero
	// value and adapters ignore it on writes.
	CachedAt time.Time `json:"cached_at,omitempty"`
}

// VideoChapter represents a single chapter slice in a video. Mirrors the
// shape produced by yt-dlp's `chapters` array.
type VideoChapter struct {
	Title     string  `json:"title"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
}

// VideoThumbnail represents a single thumbnail variant in a video. Mirrors
// the shape produced by yt-dlp's `thumbnails` array.
type VideoThumbnail struct {
	URL    string `json:"url"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// UploadResultDTO mirrors the relevant fields of infra/drive.UploadResult
// returned by *drive.Uploader.UploadFileIfChanged. Defining it here keeps
// `internal/application/youtube` from importing `internal/infrastructure/drive`.
//
// Field set is minimal — only FileID + WebViewLink — because those are the
// only fields the application Service uses (Drive file id for re-uploads,
// web link for status logging).
type UploadResultDTO struct {
	FileID      string `json:"file_id"`
	WebViewLink string `json:"web_view_link,omitempty"`
}

// SearchLiveResult is the per-video result row returned by YouTube search
// (live search via yt-dlp ytsearch). The fields are exactly what the
// application Service's SearchLive consumer needs to convert into
// `asset.Asset` instances.
type SearchLiveResult struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	URL       string  `json:"url"`
	Uploader  string  `json:"uploader"`
	Duration  float64 `json:"duration"`
	Thumbnail string  `json:"thumbnail"`
}

// VideoMetadata is an alias preserved for callers that historically
// referenced a YouTubeMetadataPort-shaped value.
type VideoMetadata = DownloaderMetadata

// YouTubeMetadataPort is the back-compat alias name for the canonical
// DownloaderMetadata DTO. Preserved so legacy callers (metadata_enrich.go,
// segment_lifecycle.go) keep compiling without rename churn.
type YouTubeMetadataPort = DownloaderMetadata

// ── Structural ports (signature-bearing) ─────────────────────────────────

// ClipStorePort is the canonical structural port for clip persistence.
// Mirrors the methods invoked from internal/application/youtube:
//   - intelligence_sync.go, enrichment.go, extractor_drive.go,
//     segment_cache.go, searcher_cache.go, rebuild_job.go, etc.
//
// Per PR3 (June 2026): added `UpsertFolder` so the same concrete
// *assets.ClipsRepository satisfies both clip-row + clip-folder-table
// operations through a single structural port.
type ClipStorePort interface {
	DB() *sql.DB
	Get(ctx context.Context, id string) (*asset.Asset, error)
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
	GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error)
	Upsert(ctx context.Context, m *asset.Asset) error
	UpsertFolder(ctx context.Context, f *asset.ClipFolder) error
	DeleteClip(ctx context.Context, id string) error
}

// MonitorsStorePort is the canonical structural port for the channel-monitor
// store. The concrete *assets.MonitorsRepository satisfies it.
type MonitorsStorePort interface {
	UpsertSource(ctx context.Context, source *asset.MonitoredSource) error
	IncrementProcessed(ctx context.Context, id string) error
}

// VideoMetadataFetcherPort fetches per-video metadata via yt-dlp dump-json
// or equivalent. Returns the canonical `*DownloaderMetadata` DTO.
//
// Concrete impl lives at `internal/infrastructure/youtube/metadata.go::MetadataFetcherAdapter`.
type VideoMetadataFetcherPort interface {
	GetVideoMetadata(ctx context.Context, videoURL string) (*DownloaderMetadata, error)
}

// DriveFolderManagerPort resolves + creates Drive folders for a channel and
// uploads local files if their content hash changed. Returns the canonical
// `*UploadResultDTO` from uploads so the application layer never imports
// `internal/infrastructure/drive`.
type DriveFolderManagerPort interface {
	GetOrCreateFolder(ctx context.Context, channelName, parentFolderID string) (string, error)
	UploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*UploadResultDTO, bool, error)
}

// FolderMemoryPort memoizes per-folder manifest state. Mirrors
// `*foldermemory.Service` so the concrete type satisfies the port directly.
type FolderMemoryPort interface {
	LoadManifest(manifestPath string) (*asset.ClipManifest, error)
	SaveManifest(manifestPath string, manifest *asset.ClipManifest) error
	UpdateManifestTXT(folder *asset.ClipFolder, manifest *asset.ClipManifest) error
	ComputeManifestStats(manifest *asset.ClipManifest) asset.ClipFolderStats
}

// OllamaClientPort is the LLM client used for scene extraction + scoring.
// Structural method set mirrors the surface of `*ollama/client.Client`.
type OllamaClientPort interface {
	SimpleGenerate(ctx context.Context, model, prompt string, timeout time.Duration, opts map[string]any) (string, error)
}

// SearchRunnerPort runs yt-dlp search CLI calls (live search + dump-json).
// Structural so application code can call SearchLive + GetVideoInfo.
//
// Concrete impl: `*infra/youtube.SearchRunnerAdapter` is wired at composition
// time from `internal/app/youtube_adapters.go::newSearchRunnerAdapter`.
type SearchRunnerPort interface {
	SearchLive(ctx context.Context, query string, limit int, sort string) ([]SearchLiveResult, error)
	GetVideoInfo(ctx context.Context, videoURL string) (*DownloaderMetadata, error)
}

// ClipIndexerPort pushes clip rows into the Qdrant vector index.
// Structural so application code can call IsEnabled + IndexClip.
type ClipIndexerPort interface {
	IsEnabled() bool
	IndexClip(ctx context.Context, id string) error
}

// ── Empty-marker ports (opaque injection tokens) ─────────────────────────

// SubtitleFetcherPort downloads + parses VTT subtitle streams for a video.
//
// Structural (was empty-marker, June 2026) because `subtitles.go::18`
// delegates to `.SliceSubtitles(ctx, videoID, startSec, endSec, outputPath)`
// when the application Service needs to slice a transcript for an extracted
// clip. Concrete impl lives in the subtitle infrastructure adapter wired at
// composition time (nil-safe; segmentation without subtitles logs a warn
// and the downstream Whisper fallback takes over).
type SubtitleFetcherPort interface {
	SliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error
}

// WhisperTranscriberPort runs Whisper transcription on audio clips.
//
// Structural (was empty-marker, June 2026) because `segment.go::218` calls
// `.TranscribeAudio(ctx, audioPath) (string, error)` to obtain a transcript
// for the freshly-cut clip. Concrete impl lives in the Whisper infrastructure
// adapter wired at composition time (currently nil-by-default; segmentation
// without Whisper skips transcript enrichment).
type WhisperTranscriberPort interface {
	TranscribeAudio(ctx context.Context, audioPath string) (string, error)
}

// ClipFilesPort manages local clip file IO (download / verify / cleanup).
//
// Structural (was empty-marker, June 2026) because the application Service
// calls `.UsableCachedClip(localPath) (bool, error)` to short-circuit
// downloads when a previously-extracted local file already passes the
// cache-eligibility check in `segment_cache.go::46`. Returning an error
// (rather than just bool) lets callers distinguish "not cached" from
// "cache check failed (IO error)" — important for telemetry on ephemeral
// disk failures vs. legitimate cache misses. Concrete impl lives in
// `internal/infrastructure/files` (or the equivalent that owns the
// `UsableCachedClip` helper).
type ClipFilesPort interface {
	UsableCachedClip(localPath string) (bool, error)
}

// HashServicePort computes MD5/SHA hashes for downloaded artefacts.
//
// Structural (was empty-marker, June 2026) because `service.go::md5String`
// and `service.go::md5File` invoke `.MD5String(data) string` and
// `.MD5File(path) (string, error)` on the wired collaborator. Concrete
// impl lives at `pkg/hashutil` (or the equivalent the application chooses)
// wired at composition time; when nil, the application falls back to
// `pkg/hashutil.MD5*` helpers transparently.
type HashServicePort interface {
	MD5String(data string) string
	MD5File(path string) (string, error)
}

// TempFileManagerPort allocates + cleans up temp files for cuts.
type TempFileManagerPort interface{}

// YouTubeCacheStorePort caches per-video derived artefacts (segments,
// search text, metadata, category). NOTE: extractor code currently uses
// `s.clips.DB()` to ad-hoc query cache tables; that pattern is preserved
// for now because the bespoke SQL statements don't yet have repo-method
// counterparts.
type YouTubeCacheStorePort interface{}
