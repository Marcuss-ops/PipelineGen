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
// Per PR2 (June 2026): the concrete `DownloaderMetadata` DTO replaced the old
// empty-marker metadata slot in `VideoCutResult.Metadata`.
package ports

import (
	"context"
	"time"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
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

// VideoCutRequest contains all parameters for downloading and cutting a video segment.
// PR5 Phase 3 (June 2026): moved from root youtube package to ports/ so both the
// extraction capability service and the root orchestrator share the same type.
type VideoCutRequest struct {
	URL               string
	VideoID           string
	Start             float64
	Duration          float64
	OutputName        string
	ForceKeyframes    bool
	KeepAudio         bool
	Normalize         bool
	Strategy          string
	OutputDir         string
	PreDownloadedPath string
}

// VideoCutResult wraps the output of a video cut operation with the local file path
// and the full video metadata captured from yt-dlp.
type VideoCutResult struct {
	LocalPath string
	Metadata  *DownloaderMetadata
}

// VideoPipelinePort is the port for downloading + cutting YouTube video segments.
// PR5 Phase 3 (June 2026): canonically defined in ports/.
type VideoPipelinePort interface {
	DownloadAndCutYouTubeVideo(ctx context.Context, req VideoCutRequest) (*VideoCutResult, error)
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

// ── Structural ports (signature-bearing) ─────────────────────────────────

type ClipStorePort interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
	GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error)
	Upsert(ctx context.Context, m *asset.Asset) error
	UpsertFolder(ctx context.Context, f *asset.ClipFolder) error
	DeleteClip(ctx context.Context, id string) error
	ListYouTubeClipIDsForSearchText(ctx context.Context, limit, offset int) ([]string, error)
	// PR3-Wave14 PR5 / PG-003 (June 2026): the youtube handler used to
	// reach through *assets.ClipsRepository for advanced search + counts;
	// now those flow through the port so the handler depends only on
	// application-layer contracts.
	SearchClipsAdvanced(ctx context.Context, req asset.AdvancedSearchRequest) (*asset.AdvancedSearchResult, error)
	CountClips(ctx context.Context) (int, error)
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

type VideoMetaRow struct {
	VideoID      string
	MetadataJSON string
}

type CachePort interface {
	GetSearch(ctx context.Context, key string) (string, bool)
	SetSearch(ctx context.Context, key, resultsJSON string)
	GetVideoMeta(ctx context.Context, videoID string) (string, bool)
	SetVideoMeta(ctx context.Context, videoID, metadataJSON string)
	BumpMetaHits(ctx context.Context, videoID string)
	PrewarmMeta(ctx context.Context, limit int) ([]VideoMetaRow, error)
	GetSegments(ctx context.Context, videoID string) (string, bool)
	SetSegments(ctx context.Context, videoID, segmentsJSON string)
	GetCategory(ctx context.Context, videoTitle string) (string, bool)
	SetCategory(ctx context.Context, videoTitle, category string)
}

// ── Commit C ports (PR-C-YouTube-Cutover, June 2026) ────────────────────

// ClipCachePort is the high-level cache check the ProcessYouTubeSegmentUseCase
// uses to detect already-processed segments and short-circuit the
// download/cut/enrich pipeline. It folds the existing CachePort +
// ClipStorePort + ClipFilesPort trio behind a single typed call so the use
// case stays at the application-layer surface and tests can drive all three
// independently via a single mock.
//
// Returns (nil, false, nil) when no cached clip exists. Returns
// (item, true, nil) when the clip is cached and authoritative for the given
// clipID. Returns (nil, false, err) when the underlying store failed.
type ClipCachePort interface {
	GetExisting(ctx context.Context, clipID string) (item *youtubetypes.ExtractItem, exists bool, err error)
}

// IndexEventPayload is the typed envelope that travels alongside the clip DB
// write in the ClipAtomicWriter transaction. It carries only routing fields
// (Type, AggregateID, CreatedAt). The outbox payload (schema_version,
// event_id, asset_id, source_version, idempotency_key) is built exclusively
// by the ClipAtomicWriter concrete adapter via BuildReindexEnvelopeV1 — the
// caller MUST NOT supply a custom payload (Blocco 1.1: the previous ad-hoc
// payload path caused every indexing event to land in dead_letter because
// the consumer rejected non-canonical payloads).
type IndexEventPayload struct {
	Type        string // e.g. "asset.index.requested"
	AggregateID string
	CreatedAt   time.Time
}

// ClipAtomicWriter is the transactional DB write + outbox-insert pair the
// ProcessYouTubeSegmentUseCase performs as its terminal step. The concrete
// implementation lives in infrastructure and owns the SQLite transaction;
// composition wires it to the new writer adapter (Commit F).
//
// Commit 2/6 (PR-C-YouTube-Cutover, June 2026, Correttezza #6): the
// signature now takes `youtubetypes.ClipAsset` (the canonical, strongly-
// typed internal domain entity) instead of `youtubetypes.ExtractItem` (the
// HTTP response shape). The verdict's P1 #6 mandates "il writer deve ricevere
// il record canonico, non un DTO di risposta HTTP" — ClipAsset bundles the
// ID/VideoID/LocalPath/FileHash/Drive/Coordinates/Metadata fields the writer
// needs in one typed struct so the DB column mapping is explicit and
// refactor-resistant.
type ClipAtomicWriter interface {
	CommitClipAndIndexEvent(ctx context.Context, clipID string, asset youtubetypes.ClipAsset, event IndexEventPayload) error
}

// ClipMetadataWriter is the port for metadata-enrichment writes. The
// concrete ClipMetadataWriterAdapter (internal/infrastructure/database/
// sqlite/assets/clip_metadata_writer.go) performs an atomic SQLite
// transaction: UPDATE media_assets.metadata_json + INSERT outbox_events
// in a single tx.
//
// Commit 4/6 (PR-C-YouTube-Cutover, June 2026, P1 #15 + #16): added
// as the canonical metadata writer port. The previous direct-assetRepo-
// -Upsert path is removed per the fail-closed posture.
type ClipMetadataWriter interface {
	UpdateClipMetadataAndRequestIndex(ctx context.Context, clipID string, m youtubetypes.CanonicalClipMetadata) error
}

// ── ffprobe validation port (audit 2026-07-03 BLOCKER #3) ───────────────

// FFProbeReport is the structured validation result from ffprobe.
// The use case reads this to decide whether the downloaded clip file
// is genuinely playable (not a corrupted/truncated download).
type FFProbeReport struct {
	ContainerReadable  bool
	VideoStreamPresent bool
	AudioPresent       bool
	DurationSeconds    float64
	Width              int
	Height             int
	FPS                float64
	Warnings           []string // non-fatal issues (e.g. FPS slightly off-template)
}

// FFProbePort validates a downloaded clip file using ffprobe.
// Nil-tolerant in the use case — when not wired, validation is
// silently skipped (the pre-existing hash + stat checks remain).
type FFProbePort interface {
	ValidateClip(ctx context.Context, localPath string, expectedDurationSec int, keepAudio bool) (*FFProbeReport, error)
}
