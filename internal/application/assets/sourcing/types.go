// Package sourcing — request/response types extracted from service.go.
//
// Per AGENTS.md Pattern 5 (June 2026): one concept per file. This file holds
// the command and result structs used by the sourcing service's public API.
package sourcing

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/sourcing"
)

// §12-5 CONTRACT (July 2026): the sourcing.IndexingStatus type-alias
// was REMOVED. Production code consumes domain.SourcingIndexStatus
// directly (canonical owner per godlike/06 SSOT). Forward-pointer:
// architecture/deprecations.yaml#PR-CrossPackage-IndexingStatus-§12-5.
//
// RegisterClipCommand is the input for registering a clip from a YouTube URL.
type RegisterClipCommand struct {
	URL         string
	Name        string
	Description string
	Tags        []string
	Source      string
	Category    string
	Group       string
	FolderID    string
	StartSec    float64
	EndSec      float64
	Force       bool
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
	FileHash       string
	Source         string
	Category       string
	Tags           []string
	LocalPath      string
	Indexed        bool
	IndexingStatus domain.SourcingIndexStatus `json:"indexing_status"`
	Transcribed    bool
	Language       string
	RelatedClips   map[string]any
	Message        string

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
type BatchClipResult struct {
	ClipID    string
	Name      string
	OK        bool
	Error     string
	Duplicate bool
}

// BatchRegisterResult is the aggregated result of a batch registration.
type BatchRegisterResult struct {
	OK        bool
	Total     int
	Succeeded int
	Failed    int
	Results   []BatchClipResult
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

// LocalToDriveCommand is the input for uploading a local folder to Drive.
type LocalToDriveCommand struct {
	LocalFolder   string
	DriveFolderID string
	Source        string
	Limit         int
	Concurrency   int
	DryRun        bool
}

// LocalToDriveResult is the output of a local-to-drive operation.
type LocalToDriveResult struct {
	OK         bool
	DryRun     bool
	JobID      string
	Message    string
	LocalFound int
	Groups     []string
}
