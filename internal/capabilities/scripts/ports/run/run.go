// Package run owns the durable generation-run port. It lives below
// scripts/ports because the DTOs are domain-bound (they embed the generate
// request and result), while the neutral run vocabulary is owned by the
// kernel. Platform adapters import this package directly instead of the
// scripts business package.
package run

import (
	"context"
	"errors"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runstate"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// GenerateRequest and GenerateResult are re-exported for the durable
// workflow checkpoint. They remain capability DTOs; this port is their only
// platform-facing surface.
type GenerateRequest = contract.GenerateRequest
type GenerateResult = contract.GenerateResult

// GenerationRun is the durable run aggregate.
type GenerationRun = runstate.GenerationRun

type RunStatus = scriptpkg.RunStatus
type Stage = scriptpkg.Stage

const (
	RunStatusPending   = scriptpkg.RunStatusPending
	RunStatusRunning   = scriptpkg.RunStatusRunning
	RunStatusCompleted = scriptpkg.RunStatusCompleted
	RunStatusFailed    = scriptpkg.RunStatusFailed

	StageNormalizing          = scriptpkg.StageNormalizing
	StagePreflight            = scriptpkg.StagePreflight
	StageGeneratingSceneText  = scriptpkg.StageGeneratingSceneText
	StageTranslatingScenes    = scriptpkg.StageTranslatingScenes
	StageGeneratingVoiceovers = scriptpkg.StageGeneratingVoiceovers
	StageCompilingAudio       = scriptpkg.StageCompilingAudio
	StagePublishingDocuments  = scriptpkg.StagePublishingDocuments
	StageCompleted            = scriptpkg.StageCompleted
	StageFailed               = scriptpkg.StageFailed
	StageCoreReady            = scriptpkg.StageCoreReady
)

// ErrRunNotFound is the canonical sentinel for "the durable repository has no
// run with that id".
var ErrRunNotFound = errors.New("scriptgeneration: run not found")

// FailRunInput carries the structured failure metadata that the runner
// persists when a stage fails. All fields except RunID and FailedStage
// are optional; implementations persist whatever non-zero fields the
// runner provides.
type FailRunInput struct {
	// RunID is the canonical run identifier (required).
	RunID string

	// FailedStage identifies which stage failed (required).
	FailedStage Stage

	// ErrorCode is a stable machine-readable error code
	// (e.g. "TEXT_GENERATION_FAILED", "TRANSLATION_FAILED",
	// "PROVIDER_TIMEOUT").
	ErrorCode string

	// ErrorMessage is a human-readable description of the failure.
	ErrorMessage string

	// AttemptCount is the current retry attempt number (0-based).
	AttemptCount int

	// NextRetryAt is the earliest time a retry should be attempted.
	// Nil means no retry is scheduled.
	NextRetryAt *time.Time
}

// RunRepository persists and retrieves GenerationRun aggregates.
type RunRepository interface {
	// Create persists a new GenerationRun.
	Create(ctx context.Context, run *GenerationRun) error

	// Get retrieves a GenerationRun by ID.
	Get(ctx context.Context, runID string) (*GenerationRun, error)

	// GetByJobID retrieves a GenerationRun associated with the given
	// worker-assigned job ID. Returns nil, nil when no run is found.
	GetByJobID(ctx context.Context, jobID string) (*GenerationRun, error)

	// UpdateStage persists the current stage and status atomically.
	// Implementations should only UPDATE the stage-relevant columns
	// (current_stage, status, updated_at) — not the full run.
	UpdateStage(ctx context.Context, runID string, status RunStatus, stage Stage) error

	// FailRun persists failure metadata for a run atomically.
	FailRun(ctx context.Context, input FailRunInput) error

	// SavePartialResult persists intermediate result data (e.g. after
	// each translated scene) so a retry can resume from the checkpoint.
	SavePartialResult(ctx context.Context, runID string, result *GenerateResult) error
}

// StaleSubmissionRun is a durable nonterminal submission whose last update is
// old enough to reconcile against the authoritative job broker.
type StaleSubmissionRun struct {
	RunID          string
	JobID          string
	IdempotencyKey string
	CurrentStage   Stage
	UpdatedAt      time.Time
}

// RunReconciliationRepository is the narrow durable surface used to repair
// submission/run correlation after a process crash. It is separate from
// RunRepository so ordinary execution does not depend on reconciliation scans.
type RunReconciliationRepository interface {
	ListStaleSubmissionRuns(context.Context, time.Time, int) ([]StaleSubmissionRun, error)
	Get(context.Context, string) (*GenerationRun, error)
	SetJobID(context.Context, string, string) error
	UpdateStage(context.Context, string, RunStatus, Stage) error
	FailRun(context.Context, FailRunInput) error
}
