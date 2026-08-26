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
	"errors"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"time"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── Domain DTOs (canonical shape at the application–infra seam) ────────────

// DownloaderMetadata is the canonical app-layer DTO produced by every
// video-metadata fetcher (yt-dlp dump-json, YouTube Data API, etc.).
type DownloaderMetadata struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	LiveStatus   string           `json:"live_status,omitempty"`
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
	URL            string
	VideoID        string
	Start          float64
	Duration       float64
	OutputName     string
	ForceKeyframes bool
	KeepAudio      bool
	Normalize      bool
	// Normalization target for callers that need a profile different from
	// the global video configuration. Zero values use the configured default.
	Strategy          string
	OutputDir         string
	PreDownloadedPath string
	// SkipMetadataFetch avoids a best-effort yt-dlp metadata subprocess on the
	// critical extraction path when the caller already supplied clip metadata.
	SkipMetadataFetch bool
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
	GetFolder(ctx context.Context, folderID string) (*detail.ClipFolder, error)
	Upsert(ctx context.Context, m *asset.Asset) error
	UpsertFolder(ctx context.Context, f *detail.ClipFolder) error
	DeleteClip(ctx context.Context, id string) error
	ListYouTubeClipIDsForSearchText(ctx context.Context, limit, offset int) ([]string, error)
	// PR3-Wave14 PR5 / PG-003 (June 2026): the youtube handler used to
	// reach through *assets.ClipsRepository for advanced search + counts;
	// now those flow through the port so the handler depends only on
	// application-layer contracts.
	SearchClipsAdvanced(ctx context.Context, req detail.AdvancedSearchRequest) (*detail.AdvancedSearchResult, error)
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
	// UploadFileIfChanged uploads a local file to the resolved Drive folder.
	// group and subject are the semantic-location fields the canonical
	// delivery.Publisher needs for YouTubeClipPath path-building
	// (group = channel/category, subject = video ID). Legacy callers
	// that use drive.Admin directly may pass empty strings — the admin
	// path ignores them.
	UploadFileIfChanged(ctx context.Context, localPath, folderID, filename, group, subject string) (*UploadResultDTO, bool, error)
}

type FolderMemoryPort interface {
	LoadManifest(manifestPath string) (*detail.ClipManifest, error)
	SaveManifest(manifestPath string, manifest *detail.ClipManifest) error
	UpdateManifestTXT(folder *detail.ClipFolder, manifest *detail.ClipManifest) error
	ComputeManifestStats(manifest *detail.ClipManifest) detail.ClipFolderStats
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
	// FetchSegmentSubtitles returns the canonical typed subtitle track
	// for [startSec, endSec]: plaintext + per-cue timings (detail.TimedCue)
	// + detected language code. The implementation probes manual
	// subtitles first then auto-generated fallback; nil/empty result is
	// a valid "not found" (NOT an error). Fetch errors are typed (network
	// failure, parse error, etc.) and propagated verbatim.
	//
	// godlike/06 SSOT: returns *detail.ResolvedTextBundle so the resolver
	// can forward the typed result without re-parsing the VTT inline.
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.a.
	FetchSegmentSubtitles(ctx context.Context, videoID string, startSec, endSec int) (*detail.ResolvedTextBundle, error)
}

type WhisperTranscriberPort interface {
	TranscribeAudio(ctx context.Context, audioPath string) (string, error)
	// TranscribeAudioWithDetection returns the typed result
	// including the model's DetectedLanguage + Confidence
	// (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b, July 2026). The
	// canonical TranscriptResult type lives in
	// internal/domain/asset (godlike/06 SSOT — one canonical owner
	// per fact) so both this application-layer port and the
	// infrastructure-layer WhisperTranscriber (in
	// internal/infrastructure/youtube/ports.go) reference the
	// same shape.
	//
	// godlike/07 no-fake-availability: the concrete adapter MUST
	// normalize DetectedLanguage to BCP-47 (lang or lang-region)
	// and collapse unknown/empty to "und" via the canonical
	// bcp47.Normalize helper. The 5-level chain in
	// TextTrackResolver.AcquireSegmentText (priority 5) calls
	// this method so the resolver can apply the
	// RequireLanguageCertainty policy gate.
	TranscribeAudioWithDetection(ctx context.Context, audioPath string) (detail.TranscriptResult, error)
}

type ClipFilesPort interface {
	UsableCachedClip(localPath string) (bool, error)
}

type HashServicePort interface {
	SHA256File(path string) (string, error)
	SHA256String(s string) string
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

// IndexEventPayload is commit metadata that travels alongside the clip DB
// write in the ClipAtomicWriter transaction. The caller cannot choose the
// outbox event type: the canonical AssetCommitter owns that responsibility
// together with the schema_version, event_id, asset_id, source_version, and
// idempotency_key fields. This prevents callers from bypassing the canonical
// asset-commit chain with an ad-hoc event envelope.
type IndexEventPayload struct {
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
// ID/VideoID/LocalPath/LegacyFileMD5/Drive/Coordinates/Metadata fields the writer
// needs in one typed struct so the DB column mapping is explicit and
// refactor-resistant.
type ClipAtomicWriter interface {
	CommitClipAndIndexEvent(ctx context.Context, clipID string, asset youtubetypes.ClipAsset, event IndexEventPayload) error
}

// ErrOutboxTerminalConflict is returned by the ClipAtomicWriter concrete
// adapter when the outbox event INSERT is suppressed by an existing
// terminal row (dead_letter or superseded) sharing the same event_key.
// The media_assets row WAS written successfully, but no new indexing
// event was created. Callers should surface this as
// "processed_but_index_blocked" — the clip exists but cannot be indexed
// until the terminal row is resolved (re-opened or a new event is created).
//
// Audit 2026-07-03 BLOCKER #4 closure: pre-closure the writer logged a
// warning and returned nil, leading to "processed" with no index event.
//
// Sentinel is defined alongside the port so both the infra adapter and
// the application-layer use case can errors.Is-probe it without an
// import cycle.
var ErrOutboxTerminalConflict = errors.New("outbox: event suppressed by existing terminal row (dead_letter or superseded); media_assets row committed, index blocked")

// ClipMetadataWriter is the port for metadata-enrichment writes. The
// concrete ClipMetadataWriterAdapter (internal/platform/sqlite/
// sqlite/assets/clip_metadata_writer.go) performs an atomic SQLite
// transaction: UPDATE media_assets.metadata_json + INSERT outbox_events
// in a single tx.
//
// Commit 4/6 (PR-C-YouTube-Cutover, June 2026, P1 #15 + #16): added
// as the canonical metadata writer port. The previous direct-assetRepo-
// -Upsert path is removed per the fail-closed posture.
type ClipMetadataWriter interface {
	UpdateClipMetadataAndRequestIndex(ctx context.Context, clipID string, m youtubetypes.CanonicalClipMetadata) error

	// UpdateClipMetadataTextsAndRequestIndex extends the metadata write
	// to also persist text tracks (transcripts, descriptions, etc.) in
	// the same atomic transaction. This ensures Qdrant never sees a
	// clip without its associated text tracks.
	//
	// When textTracks is empty, the method behaves identically to
	// UpdateClipMetadataAndRequestIndex (the text track upsert is
	// skipped). This preserves backward compatibility with callers
	// that don't carry text tracks.
	UpdateClipMetadataTextsAndRequestIndex(ctx context.Context, clipID string, m youtubetypes.CanonicalClipMetadata, textTracks []detail.TextTrack) error
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

// Step10MetricsRecorder is the application-layer port for the YouTube
// Step 10 partial-state metric (PR-PY-STEP10-FAIL-LOG-OBSEVE-PARITY,
// July 2026). The concrete adapter lives in
// internal/platform/observability/metrics_step10.go and wraps
// the Prometheus counter
// `transcript_metadata_step10_fail_after_clip_total{failure_code}`.
//
// godlike/06 SSOT: this port is the SOLE canonical application-layer
// surface for Step 10 partial-state telemetry. The use case MUST NOT
// import internal/platform/observability directly (clean
// architecture — application layer is forbidden from depending on
// infrastructure); the composition root wires the concrete adapter.
//
// godlike/07 NO-FAKE-AVAILABILITY: the contract is "exactly-once per
// Step 10 failure, with the failure_code label matching the typed
// *ExtractionError envelope's Code field". Callers MUST pass the
// stringified FailureCode constant (e.g. string(FailureCodeMetadataFailed))
// so dashboard queries can join against the typed-error taxonomy.
//
// Nil-tolerance: implementations of this port MUST be safe to invoke
// via a nil check at the use-case call site (the use case calls
// `u.deps.Step10Metrics.IncStep10FailAfterClip(...)` only when
// `u.deps.Step10Metrics != nil`). The composition root MAY wire
// the concrete adapter or omit it (the optional pattern matches
// the rest of the youtube package: Subtitles, Transcriber,
// DriveFolderMgr all gracefully degrade when nil-wired).
type Step10MetricsRecorder interface {
	IncStep10FailAfterClip(failureCode string)
}
