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
	ID             string
	Name           string
	Filename       string
	Duration       time.Duration
	Source         string
	SourceURL      string
	SourceProvider string
	SourceVideoID  string
	StartSec       float64
	EndSec         float64
	Category       string
	Tags           []string
	LocalPath      string
	DriveLink      string
	DriveFileID    string
	LegacyFileMD5       string

	// Rich metadata fields (RICH-METADATA-QDRANT-VERIFY, July 2026)
	Summary         string   `json:"summary,omitempty"`
	Topics          []string `json:"topics,omitempty"`
	Speakers        []string `json:"speakers,omitempty"`
	MentionedPeople []string `json:"mentioned_people,omitempty"`
	Hook            string   `json:"hook,omitempty"`

	// DoD #8 (July 2026): Drive folder metadata surfaced to API callers.
	// Forward-pointer PR-EXISTINGCLIP-DB-POPULATE: the SQLite adapter
	// (ClipStorePort.GetClip / FindExisting) must read these columns
	// from media_assets to populate dedup-check responses. Until then,
	// dedup hits return empty strings — honest limitation (godlike/07).
	DriveFolderID string `json:"drive_folder_id,omitempty"`
	DrivePath     string `json:"drive_path,omitempty"`
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

// ── File system ports ────────────────────────────────────────────────────
//
// PR-CLIPS-ENQUEUE-ONLY (July 2026): the FileScannerPort + LocalFileInfo
// were RETIRED. The worker is the sole owner of filesystem scanning;
// the enqueue path no longer pre-scans the directory. Callers that
// need to scan a local folder must go through the worker (which runs
// the scan when the bulk_upload_youtube_clips job executes).

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

// ── Sourcing atomic port (PR-SOURCING-ADAPTER-FAIL-CLOSED, July 2026) ──

// SourcingAtomicPort is the COMBINED canonical Pattern-0 typed surface
// for the two post-registration flows: async enrichment + Qdrant indexing
// (EnrichAndIndex) + metadata sidecar upload (UpdateCumulativeJSON).
// Centralizes the fail-closed contract so a single composition-root
// wiring decision (via wireSourcingAtomic) gates BOTH operations —
// pre-PR-SOURCING-ADAPTER-FAIL-CLOSED they were 2 independent fail-open
// paths each returning nil on unwired, masking the silent-success class
// from upstream callers (clip registration would succeed without ever
// triggering the Qdrant index request OR the metadata sidecar write).
//
// godlike/06 SSOT one-canonical-owner-per-fact: this port lives ONLY
// at internal/application/assets/sourcing/ports.go (the canonical
// typed-contract surface per the codebase's Pattern-0 discipline).
// Concrete adapters live at internal/app/youtube_adapters_meta.go
// (sourcingMetadataAdapter + sourcingEnrichmentAdapter are the canonical
// pair implementing this surface).
//
// godlike/07 typed-error contract (NO-FAKE-AVAILABILITY): implementations
// MUST return ErrSourcingUpdateCumulativeDisabled when the metadata
// sidecar handler is unwired at composition time AND ErrSourcingEnrichAndIndexDisabled
// when the enrichment handler is unwired at composition time. Returning
// nil (the pre-fix silent-success class) is a godlike/07 violation.
type SourcingAtomicPort interface {
	EnrichAndIndex(ctx context.Context, clipID, localPath, source string) error
	UpdateCumulativeJSON(ctx context.Context, tempDir, folderID, clipID string, entry map[string]any) error
}

// Typed-error contract (PR-SOURCING-ADAPTER-FAIL-CLOSED, July 2026):
// the three sentinel error values below are the canonical fail-closed
// surface for the sourcing atomic-capabilities wiring. downstream
// callers MUST probe via errors.Is(err, ErrSourcing*Disabled) etc.
// — never via raw string matches.
var (
	// ErrSourcingUpdateCumulativeDisabled surfaces when the
	// upstream caller invokes UpdateCumulativeJSON on an
	// adapter whose admin or cfg was nil at composition time
	// (the canonical Drive-not-configured / composition-bug path).
	// Caller MUST branch on errors.Is to decide whether to log
	// a soft warning (clip ledger at dispatcher is sufficient)
	// or fail-closed at the application layer.
	ErrSourcingUpdateCumulativeDisabled = errors.New(
		"sourcing: UpdateCumulativeJSON: handler disabled at composition root (admin or cfg nil — composition bug)",
	)

	// ErrSourcingEnrichAndIndexDisabled surfaces when the
	// upstream caller invokes EnrichAndIndex on an adapter
	// whose enrichment handler was nil at composition time.
	// Caller MUST branch on errors.Is to decide whether to
	// fall back to dispatch-only (Qdrant index detached) or
	// fail-closed at the application layer.
	ErrSourcingEnrichAndIndexDisabled = errors.New(
		"sourcing: EnrichAndIndex: handler disabled at composition root (clips handler nil — composition bug)",
	)

	// ErrSourcingCapabilitiesRequired surfaces at composition
	// time when cfg.Features.MediaDriveRequired == true but the
	// SourcingAtomicPort is nil. This is the load-bearing
	// fail-fast-at-composition contract: a misconfiguration must
	// surface at boot, NOT at first /api/media/register call
	// (godlike/07 fail-fast-at-boot > fail-slow-at-first-call).
	ErrSourcingCapabilitiesRequired = errors.New(
		"sourcing: SourcingAtomic capabilities required at composition (cfg.Features.MediaDriveRequired=true but handler nil)",
	)

	// ErrSourcingCapabilitiesDisabled surfaces at composition
	// time when cfg.Features.MediaDriveRequired == false AND the
	// SourcingAtomicPort is nil. Composition may continue without
	// sourcing capabilities (the canonical Drive-not-required-for-this-deployment
	// mode), but the caller sees a typed error so the deferred-at-runtime
	// fail-closed path is explicit (not a silent no-op).
	ErrSourcingCapabilitiesDisabled = errors.New(
		"sourcing: SourcingAtomic capabilities disabled (Drive not required — handler intentionally unwired)",
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
