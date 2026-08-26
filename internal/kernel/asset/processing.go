// Package asset — processing-stage type surface shared between the canonical
// kernel/asset core and the detail sub-package (extracted 2026-08-26 during the
// kernel/asset decomposition so core asset state/details can reference the
// processing lifecycle without importing the render detail package).

package asset

import "time"

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
