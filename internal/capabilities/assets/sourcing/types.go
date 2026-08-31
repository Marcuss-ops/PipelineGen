// Package sourcing — request/response types extracted from service.go.
//
// Per AGENTS.md Pattern 5 (June 2026): one concept per file. This file holds
// the command and result structs used by the sourcing service's public API.
package sourcing

import (
	domain "github.com/Marcuss-ops/PipelineGen/internal/capabilities/sourcing"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"
)

// §12-5 CONTRACT (July 2026): the sourcing.IndexingStatus type-alias
// was REMOVED. Production code consumes domain.SourcingIndexStatus
// directly (canonical owner per godlike/06 SSOT). Forward-pointer:
// architecture/deprecations.yaml#PR-CrossPackage-IndexingStatus-§12-5.
//
// RegisterClipCommand is the input for registering a clip from a YouTube URL.
//
// PR-RICH-METADATA (July 2026): added Summary, Topics, Speakers, MentionedPeople,
// Hook fields. These flow through json.Marshal/Unmarshal in the job payload
// (clipJobEnqueuerAdapter → media.clip handler) and land in
// ExistingClip → asset.Asset.Metadata → media_assets.metadata_json.
type RegisterClipCommand struct {
	URL             string
	Name            string
	Description     string
	Summary         string
	Topics          []string
	Speakers        []string
	MentionedPeople []string
	Hook            string
	Tags            []string
	Source          string
	Category        string
	Group           string
	// Location (SEMANTIC-LOCATION-API-2026-07-06 Wave 6) is the
	// canonical semantic-location DTO (godlike/06 SSOT owner:
	// internal/kernel/delivery/location.go). When non-empty,
	// the service-layer LocationResolver port (forward-pointer to
	// Wave 7) is intended to resolve this into a concrete FolderID.
	// Today the resolver port is out of scope so the service falls
	// back to the legacy FolderID when Location is set without a
	// FolderID — the typed contract is accepted at the handler
	// seam; the routing logic lands in Wave 7 composition-root
	// wiring. Per godlike/07 minimum-blast-radius the field is
	// additive: existing callers that set only FolderID continue
	// working byte-identical.
	Location domaindelivery.AssetLocationInput
	FolderID string
	StartSec float64
	EndSec   float64
	Force    bool
	// PR-YT-SECONDS-PER-SEGMENT-WIRE (July 2026, handler-side fan-out):
	// documentary on the service layer; after handler-side fan-out in
	// expandClipsBySegments each child has SecondsPerSegment=0 (stripped
	// to prevent recursive expansion). Kept on the service-DTO so the
	// existing handler->RegisterClipCommand struct literal compiles.
	// The fan-out is the handler's job; this field is informational.
	SecondsPerSegment int
	// PR-YT-NO-AUDIO-THREAD (July 2026): when true, the per-segment
	// clip uploaded to Drive has its audio track stripped at FFmpeg
	// (`-an` flag). Default false preserves the existing keep-audio
	// behavior. The field threads from handler.RegisterFromYouTubeRequest
	// -> RegisterClipCommand -> FetchRequest -> provider.Fetch
	// (forward-pointer: the concrete YouTube provider's Fetch body is
	// the canonical mapping site that translates req.NoAudio=true into
	// ProcessSegmentCommand.KeepAudio=false at the worker side).
	// godlike/07 minimum-blast-radius: existing callers omitting the
	// field stay byte-identical (false = keep audio = existing behavior).
	NoAudio bool
}

// RegisterClipResult is the output of a clip registration.
type RegisterClipResult struct {
	OK             bool
	Duplicate      bool
	ClipID         string
	VideoID        string
	Name           string
	Filename       string
	DurationSec    int
	DriveLink      string
	DriveFileID    string
	LegacyFileMD5  string
	Source         string
	Category       string
	Tags           []string
	LocalPath      string
	Indexed        bool
	IndexingStatus domain.SourcingIndexStatus `json:"indexing_status"`

	// DoD #8 (July 2026): canonical Drive folder path and folder ID
	// returned to API callers so they can see where the asset landed
	// on Drive without re-querying media_assets.
	DriveFolderID string `json:"drive_folder_id,omitempty"`
	DrivePath     string `json:"drive_path,omitempty"`
	Transcribed   bool
	Language      string
	RelatedClips  map[string]any
	Message       string

	// DeliveryStatus tracks the Drive publishing outcome (P0.2, July 2026).
	// Replaces the pre-P0.2 ambiguous "OK=true for both Drive-success and
	// Drive-failure". Canonical values: PUBLISHED / PUBLISH_FAILED /
	// LOCAL_ONLY / PUBLISH_PENDING / PUBLISHING.
	DeliveryStatus asset.AssetPublishStatus `json:"delivery_status,omitempty"`

	// RetryScheduled is true when Drive upload failed but a retry job has
	// been enqueued (P0.2, July 2026). Set alongside
	// DeliveryStatus=PUBLISH_FAILED.
	RetryScheduled bool `json:"retry_scheduled,omitempty"`
}

// BatchClipResult is the result for a single clip in a batch registration.
//
// PR-BATCH-REGISTER-ASYNC (July 2026): added JobID field; OK is always false
// (outcome unknown at enqueue time). OK=true historically meant "clip
// registered" in the synchronous path; in the async path the handler
// returns immediately with job_ids and callers MUST poll GET /api/jobs/:id
// to discover the actual outcome. Duplicate is always false in async mode.
// Empty JobID means the enqueue failed (check Error).
type BatchClipResult struct {
	ClipID    string
	Name      string
	OK        bool
	Error     string
	Duplicate bool
	JobID     string // forward-pointer PR-BATCH-REGISTER-ASYNC: async job identifier
}

// BatchRegisterResult is the aggregated result of a batch registration.
//
// PR-BATCH-REGISTER-ASYNC (July 2026): Succeeded→Enqueued, Failed→EnqueueFailed.
// Enqueued counts jobs successfully submitted to the worker queue (not clips
// that finished processing). EnqueueFailed counts per-clip enqueue errors.
// Callers poll GET /api/jobs/{id} to track actual clip registration outcomes.
type BatchRegisterResult struct {
	OK            bool
	Total         int
	Enqueued      int
	EnqueueFailed int
	Results       []BatchClipResult
}

// SyncDriveFolderCommand is the input for syncing a Drive folder.
type SyncDriveFolderCommand struct {
	DriveFolderID string
	Source        string
	Name          string
	MediaType     string
}

// SyncDriveFolderResult is the output of a sync operation.
type SyncDriveFolderResult struct {
	OK            bool
	JobID         string
	DriveFolderID string
	Source        string
	Name          string
	Message       string
}

// LocalToDriveCommand is the input for the bulk-upload-youtube-clips
// enqueue. PR-CLIPS-ENQUEUE-ONLY (July 2026): the command carries only
// the WHAT (which folder, which Drive target, source label, category).
// The HOW (recursive walk, concurrency, file patterns) is read from
// server config by the worker when the job runs. The pre-scan /
// DryRun fields are GONE — the worker is the sole owner of filesystem
// scanning.
type LocalToDriveCommand struct {
	LocalFolder   string
	DriveFolderID string
	Source        string
	Category      string
	Recursive     bool
	Concurrency   int
}

// LocalToDriveResult is the output of a local-to-drive enqueue.
// The scan results are NOT computed here — the worker emits them when
// the job runs. The handler returns this immediately with the job_id
// so the caller can poll the job status.
type LocalToDriveResult struct {
	OK      bool
	JobID   string
	Message string
}
