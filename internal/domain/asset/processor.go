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
type ProcessInput struct {
	ID               string
	Name             string
	SourceURL        string
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
type ProcessResult struct {
	ID           string
	Filename     string
	LocalPath    string
	FileHash     string
	ContentHash  string
	DriveLink    string
	DriveFileID  string
	DownloadLink string
	Status       string
	Error        string
	DuplicateOf  string
}
