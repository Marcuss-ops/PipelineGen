// Package asset — processor contracts + processing-stage type surface
// (Wave C / Phase 3 slim).
//
// Phase 3 (Wave C / Blocco 1 Asset SSOT, June 2026): the 9 SQL
// receivers that used to live here (StartProcessing/CompleteProcessing/
// FailProcessing/TransitionProcessing/GetProcessingRecord/
// GetProcessingRecordsByAssetID/GetFailedProcessingRecords/
// DeleteProcessingRecord/DeleteAllProcessingRecords) + the
// scanProcessingRecord helper + the processingRepositoryAdapter struct
// + the ProcessingRepository() factory were relocated to Local infra at
// internal/infrastructure/database/sqlite/assets/processing_queries.go,
// reachable via HYBRID-embed promotion through the legacy
// *AssetStoreSQLite struct.
//
// This file now carries ONLY the canonical type surface:
// ProcessingStatus enum, ProcessingStage enum, ProcessingRecord struct,
// Processor interface, ProcessInput/ProcessResult DTOs. NO SQL
// primitives, NO `database/sql` import.
package asset

import (
	"context"
	"time"
)

// ProcessingStatus is the 4-state lifecycle of a processing step.
type ProcessingStatus string

const (
	StatusPending   ProcessingStatus = "pending"
	StatusRunning   ProcessingStatus = "running"
	StatusCompleted ProcessingStatus = "completed"
	StatusFailed    ProcessingStatus = "failed"
)

// ProcessingStage is a canonical processing step name.
type ProcessingStage string

const (
	StageDownload      ProcessingStage = "download"
	StageNormalize     ProcessingStage = "normalize"
	StageTranscription ProcessingStage = "transcription"
	StageEmbedding     ProcessingStage = "embedding"
	StageIndexing      ProcessingStage = "indexing"
	StageUpload        ProcessingStage = "upload"
	StageVerify        ProcessingStage = "verify"
	StageCleanup       ProcessingStage = "cleanup"
)

// ProcessingRecord represents a single processing step for an asset.
type ProcessingRecord struct {
	AssetID      string           `json:"asset_id"`
	Step         string           `json:"step"`
	Status       ProcessingStatus `json:"status"`
	StartedAt    *time.Time       `json:"started_at,omitempty"`
	CompletedAt  *time.Time       `json:"completed_at,omitempty"`
	ErrorMessage string           `json:"error_message,omitempty"`
	AttemptCount int              `json:"attempt_count"`
	MetadataJSON string           `json:"metadata_json,omitempty"`
}

// Processor is the canonical interface for processing media assets.
// Concrete media processors implement this contract directly; adapters and
// package-local input/result mirrors are intentionally forbidden.
type Processor interface {
	// Process downloads, processes, and uploads an asset.
	Process(ctx context.Context, input *ProcessInput) (*ProcessResult, error)
}

// ProcessInput contains the input for processing an asset.
//
// LocalPath (Step 9/12 wire-up, July 2026): when set, Processor.Process
// SKIPS the download step and uses LocalPath as the raw source input
// for downstream processing (ffmpeg normalize, hash, upload). This lets
// the shared assets.SourceStager port REPLACE the Processor's own
// download instead of being a redundant pre-flight probe. Cleanup of
// the external file is the caller's responsibility (gateway pattern:
// the Processor does NOT delete caller-provided paths).
//
// When LocalPath is empty (the canonical legacy case), the Processor
// uses SourceURL to download. SourceURL remains the required field
// unless LocalPath is set, OR-relationship validated in Processor.Process.
type ProcessInput struct {
	ID               string
	Name             string
	SourceURL        string
	LocalPath        string
	Term             string
	OutputDir        string
	Filename         string
	FolderID         string
	Duration         int
	ForceKeyframes   bool
	StreamCopy       bool
	DownloadSections []string
	Normalize        *bool
	KeepAudio        bool
	DisableDuration  bool
	Width            int
	Height           int
	DriveFileID      string
	ClipPageURL      string
	Metadata         map[string]any
}

// ProcessResult contains the result of processing an asset.
//
// F2.8 (June 2026): MD5 + PublishAction added. The pre-F2.8 processor
// ran the upload path through drive.Uploader.UploadFile which
// returned only {FileID, WebViewLink, DownloadLink}. With the
// migration to delivery.Publisher.Publish the canonical PublishResult
// additionally surfaces {MD5Checksum, Action} — the
// Drive-calculated MD5 (the canonical "this is what Drive has stored"
// checksum, distinct from the locally-computed FileHash used for
// pre-upload dedup) AND the PublishAction enum (created/updated/
// skipped/renamed) so downstream consumers can tell whether a row
// already existed on Drive.
//
// Both fields are net-new (no omitempty since ProcessResult DTOs are
// not serialised). Pre-F2.8 callers that only relied on FileHash +
// DriveLink/DriveFileID/DownloadLink keep working unchanged. MD5 is
// "string" so delivery.PublishResult.MD5Checksum maps 1-a-1;
// PublishAction is "string" (NOT typed delivery.PublishAction) so
// the domain layer stays free of delivery-package imports (AGENTS.md
// Pattern 8: domain is the bottom of the import graph).
type ProcessResult struct {
	ID            string
	Filename      string
	LocalPath     string
	FileHash      string
	ContentHash   string
	DriveLink     string
	DriveFileID   string
	DownloadLink  string
	MD5           string // drive-returned md5Checksum (canonical "what Drive has")
	PublishAction string // created | updated | skipped | renamed | "" (unknown)
	Status        string
	Error         string
	DuplicateOf   string
}
