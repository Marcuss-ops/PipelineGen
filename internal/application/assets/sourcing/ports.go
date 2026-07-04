// Package sourcing provides application-layer use cases for sourcing media
// from external origins: YouTube clips, Drive folder sync, and local-to-Drive uploads.
package sourcing

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
)

// ── Fetch ports ────────────────────────────────────────────────────────

// FetchedAsset is the result of fetching a video from a provider.
type FetchedAsset struct {
	LocalPath string
	AssetID   string
	Name      string
	Duration  time.Duration
	Bytes     int64
	Metadata  map[string]string
}

// FetchRequest describes a video to fetch.
type FetchRequest struct {
	AssetID      string
	SourceRef    string // URL or video ID
	SegmentStart time.Duration
	SegmentEnd   time.Duration
}

// FetchProviderPort downloads a video from an external source (e.g. YouTube via yt-dlp).
type FetchProviderPort interface {
	Fetch(ctx context.Context, req FetchRequest) (*FetchedAsset, error)
}

// ── Drive ports ────────────────────────────────────────────────────────

// DriveUploadResult is the result of uploading a file to Drive.
type DriveUploadResult struct {
	FileID       string
	WebViewLink  string
	DownloadLink string
}

// DrivePort handles Drive folder operations for sourcing.
// Deprecated: new code should use PublisherPort instead.
// DRIVE-008 CUTOVER (July 2026): UploadFileWithDescription removed.
type DrivePort interface {
	GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)
	GetFolderName(ctx context.Context, folderID string) (string, error)
}

// PublisherPort is the canonical Drive publish surface for sourcing.
// It wraps delivery.Publisher and is the preferred way to upload files
// to Drive. Callers describe WHAT to publish (DestinationKey + metadata);
// the Publisher resolves WHERE it lands on Drive.
//
// Architecture rule (June 2026): new code MUST use PublisherPort instead
// of DrivePort. DrivePort is retained only for legacy callers during
// the incremental migration (FASE 5-8).
type PublisherPort interface {
	Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error)
}

// ── Clip store ports ───────────────────────────────────────────────────

// ExistingClip is the minimal info for dedup checks and result building.
type ExistingClip struct {
	ID          string
	Name        string
	Filename    string
	Duration    time.Duration
	Source      string
	Category    string
	Tags        []string
	LocalPath   string
	DriveLink   string
	DriveFileID string
	FileHash    string
}

// ClipStorePort persists and queries clip metadata.
//
// QDRANT-asset-mutation isolation (June 2026): UpsertClip was REMOVED
// from this port. Sourcing callers MUST go through IndexDispatcherPort
// (EnqueueAndIndex on outbox.Dispatcher) for any media_assets write.
// Read-side methods (FindByName, FindExisting, GetClip) stay because
// they're for dedup checks, not mutation.
type ClipStorePort interface {
	FindByName(ctx context.Context, name string) (string, error)
	FindExisting(ctx context.Context, videoID, url string, startSec, endSec float64) (string, error)
	GetClip(ctx context.Context, id string) (*ExistingClip, error)
}

// ── Jobs ports ─────────────────────────────────────────────────────────

// JobPayload is the opaque payload for a job.
type JobPayload map[string]any

// EnqueueRequest describes a background job.
type EnqueueRequest struct {
	Type       string
	Project    string
	Payload    JobPayload
	MaxRetries int
}

// EnqueuedJob is the result of enqueueing.
type EnqueuedJob struct {
	ID string
}

// JobsPort enqueues background jobs.
type JobsPort interface {
	Enqueue(ctx context.Context, req EnqueueRequest) (*EnqueuedJob, error)
}

// ── File system ports ──────────────────────────────────────────────────

// LocalFileInfo describes a file on disk found during scanning.
type LocalFileInfo struct {
	Path         string
	RelPath      string
	Name         string
	GroupName    string // e.g. actor/subdir name
	Size         int64
	MetadataPath string
	Transcript   string
}

// FileScannerPort scans local directories for media files.
type FileScannerPort interface {
	Scan(ctx context.Context, rootPath string, limit int) ([]LocalFileInfo, error)
}

// ── Hash ports ─────────────────────────────────────────────────────────

// HashPort computes file hashes.
type HashPort interface {
	MD5File(path string) (string, error)
}

// ── Transcription ports ────────────────────────────────────────────────

// TranscriptionPort transcribes audio.
type TranscriptionPort interface {
	Transcribe(ctx context.Context, audioPath string) (text string, language string, err error)
}

// ── Asset tree ports ───────────────────────────────────────────────────

// AssetTreeNode is a node in the asset tree.
type AssetTreeNode struct {
	ID        string
	Name      string
	Source    string
	ParentID  string
	DriveLink string
}

// AssetTreePort updates the asset tree.
type AssetTreePort interface {
	UpsertNode(ctx context.Context, node AssetTreeNode) error
}

// ── Search ports ───────────────────────────────────────────────────────

// SearchCandidate is a related clip from a provider.
type SearchCandidate struct {
	SourceRef string
	Title     string
	Score     float64
}

// SearchProviderPort finds clips related to a newly registered asset.
type SearchProviderPort interface {
	Search(ctx context.Context, query string, limit int) ([]SearchCandidate, error)
}

// ── Config ports ───────────────────────────────────────────────────────

// ConfigPort provides drive folder defaults.
type ConfigPort interface {
	ClipsFolder() string
	RootFolder() string
}

// ── Enrichment ports ──────────────────────────────────────────────────

// EnrichmentPort triggers async enrichment and indexing after registration.
type EnrichmentPort interface {
	EnrichAndIndex(ctx context.Context, clipID, localPath, source string) error
}

// IndexDispatcherPort is the narrow surface of outbox.Dispatcher needed by
// sourcing. EnqueueAndIndex atomically upserts the asset and enqueues an
// outbox event in a single tx — the canonical QDRANT-002 pattern.
// contentHash is the ingest-time file hash used for supersede-gate dedup.
type IndexDispatcherPort interface {
	EnqueueAndIndex(ctx context.Context, clip *ExistingClip, contentHash string) error
}

// ── Metadata upload ports ─────────────────────────────────────────────

// MetadataUploadPort uploads aggregate clip metadata JSON to Drive.
// tempDir may be empty; the adapter must handle that case gracefully.
type MetadataUploadPort interface {
	UpdateCumulativeJSON(ctx context.Context, tempDir, folderID, clipID string, entry map[string]any) error
}

// ── Shared ─────────────────────────────────────────────────────────────

// Logger is a narrow logging port.
type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
}
