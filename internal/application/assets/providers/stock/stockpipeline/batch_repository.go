// Package stockpipeline — batch_repository.go (Fase 2, July 2026).
//
// SOLE owner of the stock batch / group / artifact domain types and
// the StockBatchRepository port. The state-machine constants are
// canonical here; the SQLite-backed implementation lives in
// internal/infrastructure/database/sqlite/stockbatches.
//
// godlike/06 SSOT: every table row has a typed equivalent in this file;
// the infrastructure adapter may not invent its own entity shapes.
package stockpipeline

import (
	"context"
	"time"
)

// ArtifactState is the explicit state of a single stock artifact.
// The canonical ladder is:
//
//	PLANNED → EXTRACTING → EXTRACTED → COMPOSING → COMPOSED →
//	PUBLISHING → PUBLISHED → VERIFIED
//
// Error states:
//
//	RETRY_WAIT      — recoverable, should be retried
//	FAILED_PERMANENT — non-recoverable input/process error
//	QUARANTINED     — corruption / hash/duration mismatch, rigenerable
//	                  only after manual/automated quarantine review
//
// Transitions are atomic: callers update the row and inspect affected rows.
type ArtifactState string

// Canonical artifact states.
const (
	ArtifactStatePlanned         ArtifactState = "PLANNED"
	ArtifactStateExtracting      ArtifactState = "EXTRACTING"
	ArtifactStateExtracted       ArtifactState = "EXTRACTED"
	ArtifactStateComposing       ArtifactState = "COMPOSING"
	ArtifactStateComposed        ArtifactState = "COMPOSED"
	ArtifactStatePublishing      ArtifactState = "PUBLISHING"
	ArtifactStatePublished       ArtifactState = "PUBLISHED"
	ArtifactStateVerified        ArtifactState = "VERIFIED"
	ArtifactStateRetryWait       ArtifactState = "RETRY_WAIT"
	ArtifactStateFailedPermanent ArtifactState = "FAILED_PERMANENT"
	ArtifactStateQuarantined     ArtifactState = "QUARANTINED"
)

// BatchState is the top-level state of a stock batch.
type BatchState string

// Canonical batch states.
const (
	BatchStatePlanned   BatchState = "PLANNED"
	BatchStateRunning   BatchState = "RUNNING"
	BatchStateSucceeded BatchState = "SUCCEEDED"
	BatchStateFailed    BatchState = "FAILED"
	BatchStateRetryWait BatchState = "RETRY_WAIT"
)

// GroupState is the state of a stock batch group (e.g. round).
type GroupState string

// Canonical group states.
const (
	GroupStatePlanned   GroupState = "PLANNED"
	GroupStateRunning   GroupState = "RUNNING"
	GroupStateSucceeded GroupState = "SUCCEEDED"
	GroupStateFailed    GroupState = "FAILED"
	GroupStateRetryWait GroupState = "RETRY_WAIT"
)

// StockBatch is the canonical row shape of stock_batches.
type StockBatch struct {
	ID             string
	Fingerprint    string
	SourceURL      string
	SourceCacheKey string
	RootFolderID   string
	RootFolderName string
	Status         BatchState
	ExpectedGroups int
	ExpectedClips  int
	VerifiedClips  int
	PolicyVersion  string
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// StockBatchGroup is the canonical row shape of stock_batch_groups.
type StockBatchGroup struct {
	ID            string
	BatchID       string
	GroupKey      string
	Title         string
	FolderName    string
	DriveFolderID string
	StartSec      float64
	EndSec        float64
	ExpectedClips int
	VerifiedClips int
	Status        GroupState
	ChildJobID    string
	Attempts      int
	LastError     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// StockArtifact is the canonical row shape of stock_artifacts.
type StockArtifact struct {
	ID                 string
	BatchID            string
	GroupID            string
	Ordinal            int
	ArtifactKey        string
	SourceURL          string
	StartSec           float64
	EndSec             float64
	ExpectedDurationMs int
	ActualDurationMs   int
	LocalPath          string
	SHA256             string
	Status             ArtifactState
	DriveFileID        string
	DriveFolderID      string
	DriveLink          string
	Attempts           int
	LastError          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// StockBatchRepository is the application-layer port for durable stock
// batch state. Production wiring supplies the SQLite-backed adapter;
// nil is allowed for tests and back-compat paths.
type StockBatchRepository interface {
	// Batch CRUD / lifecycle.
	CreateBatch(ctx context.Context, batch *StockBatch) error
	GetBatch(ctx context.Context, id string) (*StockBatch, error)
	UpdateBatchStatus(ctx context.Context, id string, status BatchState, lastError string) error

	// Group CRUD / lifecycle.
	CreateGroup(ctx context.Context, group *StockBatchGroup) error
	GetGroup(ctx context.Context, id string) (*StockBatchGroup, error)
	UpdateGroupStatus(ctx context.Context, id string, status GroupState, lastError string) error

	// Artifact CRUD / lifecycle.
	CreateArtifact(ctx context.Context, artifact *StockArtifact) error
	GetArtifact(ctx context.Context, id string) (*StockArtifact, error)

	// MarkArtifactExtracting transitions an artifact from PLANNED/RETRY_WAIT
	// to EXTRACTING and bumps attempts. It fails if the artifact is already
	// being processed by another worker (race-safe).
	MarkArtifactExtracting(ctx context.Context, id string) error
	// MarkArtifactExtracted transitions an artifact from EXTRACTING to EXTRACTED
	// and persists the produced file path, SHA-256 and actual duration.
	MarkArtifactExtracted(ctx context.Context, id, localPath, sha256 string, actualDurationMs int) error
	// MarkArtifactFailed transitions an artifact from EXTRACTING to the given
	// error state (RETRY_WAIT, FAILED_PERMANENT or QUARANTINED) and records
	// the last error.
	MarkArtifactFailed(ctx context.Context, id string, status ArtifactState, lastError string) error

	// FindIncompleteArtifacts returns artifacts of a group that are not yet
	// terminal (VERIFIED / FAILED_PERMANENT / QUARANTINED), ordered by ordinal.
	// Only artifacts with attempts < maxAttempts are returned.
	FindIncompleteArtifacts(ctx context.Context, groupID string, maxAttempts int) ([]StockArtifact, error)
}
