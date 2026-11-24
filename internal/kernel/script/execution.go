package script

import (
	"context"
	"fmt"
	"time"

	audiolang "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"
	"github.com/google/uuid"
)

// ExecutionContext is the immutable correlation envelope propagated through
// every script-generation step. JobID is the current execution identity;
// RootJobID remains stable across child/retry work.
type ExecutionContext struct {
	RootJobID     string
	JobID         string
	ParentJobID   string
	ProjectID     string
	VideoID       string
	CorrelationID string
	Attempt       int
}

func (c ExecutionContext) Validate() error {
	if c.JobID == "" || c.RootJobID == "" || c.CorrelationID == "" {
		return fmt.Errorf("execution context requires job_id, root_job_id, and correlation_id")
	}
	return nil
}

// NewExecutionContext creates the default single-job correlation envelope.
func NewExecutionContext(jobID, correlationID string) ExecutionContext {
	if correlationID == "" {
		correlationID = uuid.NewString()
	}
	return ExecutionContext{RootJobID: jobID, JobID: jobID, CorrelationID: correlationID, Attempt: 1}
}

type ExecutionStep struct {
	StepID       string
	Name         string
	Type         string
	Status       string
	StartedAt    time.Time
	CompletedAt  time.Time
	DurationMS   int64
	ErrorMessage string
}

// ArtifactOperation kinds — the stable vocabulary every phase uses when it
// records an artifact operation, so a query can join translation → TTS →
// subtitles → render → validation → Drive on one consistent operation kind.
const (
	OperationTranslation = "translation"
	OperationTTS         = "tts"
	OperationSubtitles   = "subtitles"
	OperationRender      = "render"
	OperationValidation  = "validation"
	OperationDriveUpload = "drive_upload"
)

// ArtifactOperation is the end-to-end correlation key for one traceable
// operation that produces, transforms, or validates an artifact. JobID rides
// on the ExecutionContext; the remaining key fields (scene_id, language,
// asset_id, operation_id) travel on this struct so a question like "why was
// Spanish Scene 4 not uploaded?" can be answered by joining every phase on
// the same key across translation → TTS → subtitles → render → validation →
// Drive.
type ArtifactOperation struct {
	// OperationID is the stable per-attempt identifier of the operation
	// (e.g. "translation:scene-4:es:attempt-1").
	OperationID string `json:"operation_id"`
	// Kind is one of the Operation* constants (translation/tts/subtitles/
	// render/validation/drive_upload).
	Kind string `json:"kind"`
	// SceneID is empty for run-scoped artifacts (final audio, document).
	SceneID string `json:"scene_id,omitempty"`
	// Language is empty for run-scoped artifacts.
	Language audiolang.Language `json:"language,omitempty"`
	// AssetID is the produced artifact identity; empty for text-only
	// operations (translation).
	AssetID string `json:"asset_id,omitempty"`
	// Status is COMPLETED for a recorded produced/validated artifact.
	Status string `json:"status"`
}

// ExecutionRecorder is the technology-independent lineage/step port. It is
// deliberately narrower than the Job Registry adapter; the pipeline never
// imports SQLite or a provider-specific recorder.
type ExecutionRecorder interface {
	StartStep(context.Context, ExecutionContext, ExecutionStep) error
	CompleteStep(context.Context, ExecutionContext, ExecutionStep) error
	FailStep(context.Context, ExecutionContext, ExecutionStep, error) error
	AttachInputAsset(context.Context, ExecutionContext, string, string, int) error
	AttachOutputAsset(context.Context, ExecutionContext, string, string, int) error
	RecordMetric(context.Context, ExecutionContext, string, string, float64, string) error
	// RecordOperation records one artifact operation with its full
	// correlation key (job_id from ExecutionContext + scene_id + language +
	// asset_id + operation_id).
	RecordOperation(context.Context, ExecutionContext, ArtifactOperation) error
}
