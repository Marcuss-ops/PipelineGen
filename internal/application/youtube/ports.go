// Package youtube — application-layer port interfaces (PR1.7).
//
// Per PR1.7 (June 2026): the setter cascade on the YouTube Service is
// collapsed into a single NewService(ServiceDeps) constructor. The deps
// struct references the port interfaces declared below; wiring is done
// wholesale at composition time instead of one SetXxx call per field.
//
// Per the PR1.7 followup (June 2026): ClipStorePort and MonitorsStorePort
// are now SHAPE-bearing structural interfaces, not empty markers. They
// mirror the methods the application actually calls so the structural
// type assertion `var _ ClipStorePort = (*assets.ClipsRepository)(nil)`
// catches signature drift at compile time (PR1.5 invariant restored).
//
// All other ports remain empty markers because the application Service
// never calls methods on them directly — concrete collaborators
// (typically in internal/infrastructure/youtube) satisfy them
// structurally and the wire type is only used as opaque injection.
package youtube

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Structural ports (rename + access pattern requires real signatures) ──

// ClipStorePort is the canonical structural port for clip persistence.
// Mirrors the methods invoked from internal/application/youtube:
//
//   - intelligence_sync.go, enrichment.go, extractor_drive.go,
//     segment_cache.go, searcher_cache.go, rebuild_job.go, etc.
//
// The concrete *assets.ClipsRepository satisfies it (compile-time asserted
// in internal/infrastructure/database/sqlite/assets/clips_repository.go).
//
// `DB() *sql.DB` is intentionally retained so consumers that run ad-hoc
// cache-table queries (youtube_category_cache, youtube_segments_cache,
// youtube_search_cache, youtube_video_metadata_cache) keep compiling
// without forcing a full repository refactor of every bespoke cache call.
// Those embedded SQL statements are out of scope for the PR1.7 followup
// and warrant a dedicated repo-method migration later.
type ClipStorePort interface {
	DB() *sql.DB
	Get(ctx context.Context, id string) (*asset.Asset, error)
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
	GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error)
	Upsert(ctx context.Context, m *asset.Asset) error
	DeleteClip(ctx context.Context, id string) error
}

// MonitorsStorePort is the canonical structural port for the channel-monitor
// store. The concrete *assets.MonitorsRepository satisfies it.
//
// Method set is the minimum required by extractor.go + extractor_drive.go;
// fuller methods (GetByExternalURL, ListDue, MarkChecked) remain unused by
// the application surface and are deliberately omitted.
type MonitorsStorePort interface {
	UpsertSource(ctx context.Context, source *asset.MonitoredSource) error
	IncrementProcessed(ctx context.Context, id string) error
}

// ── Empty-marker ports ──

// SearchRunnerPort runs yt-dlp search/info CLI calls (live search + dump-json).
type SearchRunnerPort interface{}

// SubtitleFetcherPort downloads + parses VTT subtitle streams for a video.
type SubtitleFetcherPort interface{}

// WhisperTranscriberPort runs Whisper transcription on audio clips.
type WhisperTranscriberPort interface{}

// ClipFilesPort manages local clip file IO (download / verify / cleanup).
type ClipFilesPort interface{}

// VideoMetadataFetcherPort fetches per-video metadata (title, description,
// chapters, thumbnails) via yt-dlp dump-json or equivalent.
type VideoMetadataFetcherPort interface{}

// DriveFolderManagerPort resolves + creates Drive folders for a channel.
type DriveFolderManagerPort interface{}

// HashServicePort computes MD5/SHA hashes for downloaded artefacts.
type HashServicePort interface{}

// TempFileManagerPort allocates + cleans up temp files for cuts.
type TempFileManagerPort interface{}

// YouTubeCacheStorePort caches per-video derived artefacts (segments, search text).
type YouTubeCacheStorePort interface{}

// ClipIndexerPort pushes clip rows into the Qdrant vector index.
type ClipIndexerPort interface{}

// FolderMemoryPort memoizes per-folder Drive IDs to avoid repeat lookups.
type FolderMemoryPort interface{}

// OllamaClientPort is the LLM client used for scene extraction + scoring.
type OllamaClientPort interface{}

// YouTubeMetadataPort is the typed carrier for per-video metadata returned
// to callers of DownloadAndCut (VideoCutResult.Metadata). Concrete impls are
// produced by infra/youtube adapters (see
// internal/infrastructure/youtube/metadata.go's YouTubeMetadata struct).
type YouTubeMetadataPort interface{}
