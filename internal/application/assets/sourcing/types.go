// Package sourcing — request/response types extracted from service.go.
//
// Per AGENTS.md Pattern 5 (June 2026): one concept per file. This file holds
// the command and result structs used by the sourcing service's public API.
package sourcing

import (
	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/sourcing"
)

// IndexingStatus is the canonical type-alias mirroring
// `domain.SourcingIndexStatus`. The application-layer historical name
// "IndexingStatus" is preserved so the 4 §12-5 BACKFILL-touched sites
// (architecture/issues.yaml#PR-CROSSPACKAGE-INDEXING-STATUS-§12-5:
// types.go:39 + youtube/service.go (2 sites) + youtube/ports.go (doc) +
// register/handler.go:126) continue to compile unchanged.
//
// godlike/06 SSOT: production ownership of the lifecycle enum is in
// internal/domain/sourcing/index_status.go. This alias is a TRANSPARENT
// Go type-alias (no parallel state, no forked serialization). Type-identity
// is locked via compile-time assertions in types_test.go (the user-facing
// contract: `var _ domain.SourcingIndexStatus = IndexingStatus("")`).
//
// Wire breaking change vs the pre-§12-5 placeholder strings
// ("enqueued"/"not_configured"): the canonical enum emits
// "pending"/"skipped"/"completed"/"failed". The migration is
// implemented in internal/application/assets/sourcing/youtube/helpers.go
// (IndexStatus return type changes from string to domain.SourcingIndexStatus).
type IndexingStatus = domain.SourcingIndexStatus

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
	IndexingStatus IndexingStatus `json:"indexing_status"`
	Transcribed    bool
	Language       string
	RelatedClips   map[string]any
	Message        string
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
