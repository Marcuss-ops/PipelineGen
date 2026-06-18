// Package asset defines the canonical domain types for the asset subsystem.
//
// Processing tracks the lifecycle of discrete processing steps (download,
// normalize, transcription, embedding, indexing, upload, verify, cleanup)
// for a media asset. Each (asset_id, step) pair is an independent state
// machine with 4 states: pending → running → completed/failed.
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
// Callers SHOULD use these constants but MAY pass any string;
// the repository does not validate the step name.
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

// ProcessingRecord represents a single asset_processing row.
type ProcessingRecord struct {
	AssetID       string
	Step          string
	Status        ProcessingStatus
	StartedAt     *time.Time
	CompletedAt   *time.Time
	ErrorMessage  string
	AttemptCount  int
	MetadataJSON  string
}

// ProcessingRepository is the canonical domain contract for persisting
// processing step state. Implementations live in the infrastructure layer.
type ProcessingRepository interface {
	// Start transitions a step to 'running'. Creates the record if it
	// doesn't exist (idempotent). Increments attempt_count on re-start.
	Start(ctx context.Context, assetID, step string) error

	// Complete marks a running step as completed. Returns error if the
	// step is not in 'running' state.
	Complete(ctx context.Context, assetID, step string) error

	// Fail marks a running step as failed with an error message.
	// Returns error if the step is not in 'running' state.
	Fail(ctx context.Context, assetID, step, errMsg string) error

	// Transition atomically transitions a step from one status to another.
	// Returns error if the current status doesn't match 'from'.
	Transition(ctx context.Context, assetID, step string, from, to ProcessingStatus) error

	// Get returns a single processing record for an asset + step, or nil.
	Get(ctx context.Context, assetID, step string) (*ProcessingRecord, error)

	// GetByAssetID returns all processing records for an asset.
	GetByAssetID(ctx context.Context, assetID string) ([]ProcessingRecord, error)

	// GetFailed returns all failed processing records across all assets.
	GetFailed(ctx context.Context) ([]ProcessingRecord, error)

	// Delete removes a single processing record.
	Delete(ctx context.Context, assetID, step string) error

	// DeleteAll removes all processing records for an asset.
	DeleteAll(ctx context.Context, assetID string) error
}
