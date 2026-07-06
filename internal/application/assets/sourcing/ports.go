// Package sourcing provides application-layer use cases for sourcing media
// from external origins: YouTube clips, Drive folder sync, and local-to-Drive uploads.
package sourcing

import (
	"context"
	"errors"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/domain/delivery"
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
	// PR-YT-NO-AUDIO-THREAD (July 2026): when true, the per-segment
	// clip uploaded to Drive has its audio track stripped at FFmpeg.
	// Default false preserves the existing keep-audio behavior. Threads
	// from RegisterClipCommand.NoAudio through the provider boundary;
	// the concrete YouTube provider's Fetch body is the canonical
	// mapping site that translates this into ProcessSegmentCommand.KeepAudio
	// (worker-side canonical: pointer-to-bool with omitempty semantics).
	// godlike/07 minimum-blast-radius: zero-value false = existing
	// behavior; new no-audio wire users opt-in explicitly.
	NoAudio bool
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

// Legacy sourcing.DrivePort was retired in FASE 0.3 (July 2026) per
// PR-YT-DRIVE-LEGACY-RETIRE (godlike/07 no-fake-availability: zero
// live concrete remained). The canonical Publisher-port path
// (delivery.Publisher.Publish, FASE 5 since June 2026) is the sole
// Drive upload canal for the YouTube registrar. See
// architecture/deprecations.yaml#PR-YT-DRIVE-LEGACY-RETIRE for the
// full retirement audit-trail.

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
//
// PR-RICH-METADATA (July 2026): added Summary, Topics, Speakers,
// MentionedPeople, Hook fields. These flow through the sourcing
// adapter chain into asset.Asset.Metadata → media_assets.metadata_json
// for Qdrant semantic search enrichment.
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

	// Rich metadata fields (RICH-METADATA-QDRANT-VERIFY, July 2026)
	Summary         string   `json:"summary,omitempty"`
	Topics          []string `json:"topics,omitempty"`
	Speakers        []string `json:"speakers,omitempty"`
	MentionedPeople []string `json:"mentioned_people,omitempty"`
	Hook            string   `json:"hook,omitempty"`
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

// ── Location resolver ports (PR-RESOLVER-PORT-EXTRACT, SEMANTIC-LOCATION-API Wave 7) ──

// Typed-error contract (godlike/07 NO-FAKE-AVAILABILITY): the port is the
// canonical owner of the three sentinel error values below. downstream
// callers MUST probe via errors.Is(err, ErrLocationResolverEmpty) etc.
// — never via raw string matches.
var (
	// ErrLocationResolverEmpty surfaces when the resolver is asked to
	// resolve a zero-value AssetLocationInput. Empty inputs cannot be
	// resolved to a Drive folder because the resolver has zero
	// discriminators (category / subject / project etc.). The caller
	// MUST pre-check via AssetLocationInput.IsEmpty() before invoking
	// Resolve — this sentinel surfaces late as a sanity guard against
	// gates that were bypassed.
	ErrLocationResolverEmpty = errors.New(
		"sourcing: LocationResolver.Resolve called with empty AssetLocationInput",
	)

	// ErrLocationResolverDestinationUnsupported surfaces when the
	// resolver is asked to resolve a (Location, DestinationKey) pair
	// for which the canonical per-destination mapping table does not
	// define a segment shape. Future destination registrations MUST
	// extend the table here AND extend BuildPublishRequest's switch
	// statement in lockstep (godlike/06 2-surface lockstep).
	ErrLocationResolverDestinationUnsupported = errors.New(
		"sourcing: LocationResolver.Resolve: destination does not support semantic-location mapping",
	)

	// ErrLocationResolverIncompatibleFields surfaces when the
	// input carries a field that is mutually-exclusive with the
	// destination (e.g. Style on DestinationYouTubeClip which
	// does not consume it under BuildPublishRequest's mapping).
	// The caller MUST drop the non-applicable fields rather than
	// hoping the resolver silently ignores them (godlike/07
	// typed-error contract: no silent fallback to a half-formed
	// PublishRequest).
	ErrLocationResolverIncompatibleFields = errors.New(
		"sourcing: LocationResolver.Resolve: location carries fields incompatible with destination",
	)
)

// LocationResolverPort resolves a canonical semantic-location DTO
// into a concrete Drive folder ID for a given destination.
//
// SEMANTIC-LOCATION-API-2026-07-06 Wave 7 (PR-RESOLVER-PORT-EXTRACT):
// the port is the canonical Pattern-0 typed contract that consumes
// internal/domain/delivery.AssetLocationInput (godlike/06 SSOT owner:
// internal/domain/delivery/location.go) and returns a folder-id string.
// Downstream YouTubeRegistrar / BatchRegistrar sub-services MUST NOT
// build a folder-id from raw Location fields directly — they invoke
// this port and merge the resolved id into RegisterClipCommand.FolderID
// per the F3 service-layer fallback contract.
//
// godlike/07 typed-error contract: implementations MUST return one of
// the typed sentinels above (or wrap them via dual-%w per Go 1.20+)
// when the input is empty, the destination is unsupported, or the
// input carries incompatible fields. Returning a raw `fmt.Errorf` is
// a godlike/07 violation — callers cannot probe typed errors.
//
// godlike/06 SSOT one-canonical-owner-per-fact: this port lives
// ONLY in internal/application/assets/sourcing/ports.go. Concrete
// adapters live in internal/infrastructure/drive/resolver/ (C3
// hybrid — interface in app, adapter in infra).
type LocationResolverPort interface {
	Resolve(ctx context.Context, loc domaindelivery.AssetLocationInput, dest delivery.DestinationKey) (folderID string, err error)
}
