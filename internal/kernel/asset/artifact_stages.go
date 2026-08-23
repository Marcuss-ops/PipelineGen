package asset

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ArtifactStageState is the lifecycle state of one artifact publication record.
type ArtifactStageState string

const (
	ArtifactStageStateStaged          ArtifactStageState = "STAGED"
	ArtifactStageStatePublished       ArtifactStageState = "PUBLISHED"
	ArtifactStageStateSucceeded       ArtifactStageState = "SUCCEEDED"
	ArtifactStageStateFailedPermanent ArtifactStageState = "FAILED_PERMANENT"
)

func (st ArtifactStageState) IsValid() bool {
	switch st {
	case ArtifactStageStateStaged, ArtifactStageStatePublished, ArtifactStageStateSucceeded, ArtifactStageStateFailedPermanent:
		return true
	default:
		return false
	}
}

func (st ArtifactStageState) String() string { return string(st) }

func (st ArtifactStageState) IsTerminal() bool {
	return st == ArtifactStageStateSucceeded || st == ArtifactStageStateFailedPermanent
}

// Requirement states whether an artifact is needed for the parent job to succeed.
type Requirement string

const (
	RequirementOptional Requirement = "optional"
	RequirementRequired Requirement = "required"
)

func (r Requirement) IsValid() bool {
	return r == RequirementOptional || r == RequirementRequired
}

func (r Requirement) String() string { return string(r) }

// ArtifactStage is the canonical per-publication record persisted in artifact_stages.
type ArtifactStage struct {
	ID                string
	JobID             string
	LocalPath         string
	Hash              string
	Size              int64
	Mime              string
	Requirement       Requirement
	Destination       string
	State             ArtifactStageState
	AttemptCount      int
	LastError         string
	PublishedLocation string
	PublishedAt       *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

var (
	ErrInvalidArtifactStageState = errors.New("artifact_stages: invalid state (not in canonical ArtifactStageState set)")
	ErrInvalidRequirement        = errors.New("artifact_stages: invalid requirement (not in canonical set)")
	ErrInvalidArtifactStageID    = errors.New("artifact_stages: ID is required")
	ErrInvalidJobID              = errors.New("artifact_stages: JobID is required")
	ErrArtifactStageNotFound     = errors.New("artifact_stages: stage not found")
	ErrArtifactRequiredMissing   = errors.New("artifact_stages: required artifact missing or permanently failed")
	ErrQuotaExceeded             = errors.New("artifact_stages: quota exceeded")
	ErrDiskSpaceLow              = errors.New("artifact_stages: free disk space below minimum")
	ErrArtifactStageHashMismatch = errors.New("artifact_stages: hash mismatch")
	ErrArtifactStageIDCollision  = errors.New("artifact_stages: ID collision")
	ErrArtifactStageEmpty        = errors.New("artifact_stages: empty artifact")
	ErrOutboxEmit                = errors.New("artifact_stages: outbox event emission failed")
	ErrTerminalStateRejection    = errors.New("artifact_stages: terminal-state transition rejected")
)

func WrapArtifactStageNotFound(id string) error {
	return Wrap(ErrArtifactStageNotFound, "id=%s", id)
}

func WrapArtifactRequiredMissing(jobID, requirement, id string) error {
	return Wrap(ErrArtifactRequiredMissing, "job_id=%s requirement=%s id=%s", jobID, requirement, id)
}

func Wrap(sentinel error, format string, args ...any) error {
	return wrappedError{sentinel: sentinel, msg: fmt.Sprintf(format, args...)}
}

type wrappedError struct {
	sentinel error
	msg      string
}

func (e wrappedError) Error() string { return e.sentinel.Error() + ": " + e.msg }
func (e wrappedError) Unwrap() error { return e.sentinel }

// ArtifactStageRepository is the persistence port for the artifact_stages saga table.
type ArtifactStageRepository interface {
	Insert(ctx context.Context, stage *ArtifactStage) error
	GetByID(ctx context.Context, id string) (*ArtifactStage, error)
	ListByJob(ctx context.Context, jobID string) ([]ArtifactStage, error)
	ListByState(ctx context.Context, state ArtifactStageState, limit int) ([]ArtifactStage, error)
	MarkPublished(ctx context.Context, id, publishedLocation string, publishedAt time.Time) error
	MarkSucceeded(ctx context.Context, id string) error
	MarkFailedPermanent(ctx context.Context, id, lastError string) error
	IncrementAttemptCount(ctx context.Context, id string) error
	InsertWithOutbox(ctx context.Context, stage *ArtifactStage, eventType string, payload []byte) (eventKey string, err error)
}
