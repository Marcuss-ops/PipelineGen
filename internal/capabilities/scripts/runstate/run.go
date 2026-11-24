// run.go — durable run aggregate: GenerationRun, run/retry
// state machine and error sentinels (split from model.go).
package runstate

import (
	"errors"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptrun "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type GenerationRun struct {
	// ID is the canonical run identifier (pipeline_run.id).
	ID string `json:"id"`

	// JobID is the canonical worker-assigned job identifier, set
	// after the submission service returns. Used by GET /full to
	// correlate the job with its generation run.
	JobID string `json:"job_id,omitempty"`

	// Request is the original generation request.
	Request contract.GenerateRequest `json:"request"`

	// Status is the high-level lifecycle status.
	Status RunStatus `json:"status"`

	// CurrentStage identifies the exact workflow phase.
	CurrentStage Stage `json:"current_stage"`

	// Result is populated when the run reaches a terminal state.
	Result *contract.GenerateResult `json:"result,omitempty"`

	// ErrorCode, ErrorMessage, and FailedStage capture the failure reason.
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	FailedStage  Stage  `json:"failed_stage,omitempty"`

	// RetryCount tracks how many times this run has been retried.
	AttemptCount int `json:"attempt_count"`

	// NextRetryAt is the earliest time a retry should be attempted.
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`

	// CreatedAt and UpdatedAt track the run lifecycle.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RunStatus is the high-level lifecycle status of a GenerationRun. The
// vocabulary is owned by the kernel so platform adapters can decode a durable
// run without importing the scripts business package.
type RunStatus = scriptrun.RunStatus

const (
	RunStatusPending   = scriptrun.RunStatusPending
	RunStatusRunning   = scriptrun.RunStatusRunning
	RunStatusCompleted = scriptrun.RunStatusCompleted
	RunStatusFailed    = scriptrun.RunStatusFailed
)

// Stage identifies the exact phase within the workflow.
type Stage = scriptrun.Stage

const (
	StageNormalizing          = scriptrun.StageNormalizing
	StagePreflight            = scriptrun.StagePreflight
	StageGeneratingSceneText  = scriptrun.StageGeneratingSceneText
	StageTranslatingScenes    = scriptrun.StageTranslatingScenes
	StageGeneratingVoiceovers = scriptrun.StageGeneratingVoiceovers
	StageCompilingAudio       = scriptrun.StageCompilingAudio
	StagePublishingDocuments  = scriptrun.StagePublishingDocuments
	StageCompleted            = scriptrun.StageCompleted
	StageFailed               = scriptrun.StageFailed
	StageCoreReady            = scriptrun.StageCoreReady
)

// ErrProjectRequired is the typed sentinel surfaced when a
// voiceover-enabled generation reaches the voiceover phase with no resolved
// Project. The runner fails the run BEFORE the first TTS call instead of
// letting the publisher fail after TTS work is already spent
// (godlike/07 NO-FAKE-AVAILABILITY — no silent "scene" namespace fallback).
var ErrProjectRequired = errors.New("scriptgeneration: Project is required for voiceover-enabled generation (resolve it once via BuildGenerateRequest before the voiceover phase)")

// ErrEmptyGeneratedText identifies a durable generation that produced no
// narration at all. Word targets and minimums do not gate completion.
var ErrEmptyGeneratedText = errors.New("scriptgeneration: generated narration is empty")

// ── Document config helper ──────────────────────────────────────────

// ErrGoogleDocsRetired is the typed sentinel for a generate request that still
// asks for Google Docs publication. The capability — document rendering, the
// Google Docs client, the deferred `script.docs_publish` child job and the
// localization document assembler — was removed completely. Keeping the
// request fail-closed means a caller never believes a document was produced
// when it was not (godlike/07 NO-FAKE-AVAILABILITY).
var ErrGoogleDocsRetired = errors.New("scriptgeneration: Google Docs publication has been retired; omit the docs block or set docs.enabled=false")

// ── Completion contract ─────────────────────────────────────────────

// IsCoreCompletable checks whether the CORE half of a run is durable.
//
// The retired completion predicates (IsCoreCompletable / IsRunCompletable)
// were removed with the Google Docs capability. CORE_READY was only ever a
// milestone for the deferred docs leg; a run now completes after the
// non-document phases, and COMPLETED keeps its meaning (every requested
// artifact is persisted).

// ── Retry helpers ───────────────────────────────────────────────────

// MaxRetries is the maximum number of retry attempts per run.
const MaxRetries = 3

// RetryDelay computes the exponential backoff delay for the given
// attempt number. Base delay is 5 seconds, doubling each retry
// up to a max of 120 seconds.
func RetryDelay(attempt int) time.Duration {
	d := time.Duration(5<<uint(attempt)) * time.Second
	if d > 120*time.Second {
		d = 120 * time.Second
	}
	return d
}

// ShouldRetry checks whether a failed run should be retried based on
// the attempt count and current retry schedule.
func ShouldRetry(run *GenerationRun) bool {
	if run == nil {
		return false
	}
	if run.Status != RunStatusFailed && run.Status != RunStatusRunning {
		return false
	}
	if run.AttemptCount >= MaxRetries {
		return false
	}
	if run.NextRetryAt != nil && run.NextRetryAt.After(time.Now()) {
		return false
	}
	return true
}

// stageOrder defines the canonical stage execution order for
// resume-from-checkpoint logic. Each stage maps to its index
// in the ordered list.
// StageOrder is the canonical stage execution order.
var StageOrder = []Stage{
	StageNormalizing,
	StagePreflight,
	StageGeneratingSceneText,
	StageTranslatingScenes,
	StageGeneratingVoiceovers,
	StageCompilingAudio,
	StagePublishingDocuments,
}

// StageIndex returns the zero-based index of a stage in the
// canonical execution order, or -1 if the stage is not in the
// order (e.g. terminal stages like COMPLETED/FAILED).
func StageIndex(s Stage) int {
	for i, st := range StageOrder {
		if st == s {
			return i
		}
	}
	return -1
}

// ResumeFrom returns the first non-terminal stage that should be
// executed. If the run is COMPLETED, returns StageCompleted.
// If the run failed at a specific stage, returns that stage.
// Otherwise returns StageNormalizing.
func ResumeFrom(run *GenerationRun) Stage {
	if run == nil {
		return StageNormalizing
	}
	switch run.Status {
	case RunStatusCompleted:
		return StageCompleted
	case RunStatusFailed:
		// A worker can be canceled at the CORE_READY boundary before the
		// final-job receipt is checkpointed. In that case the result is still
		// durable and CORE_READY is the strongest resume marker available.
		// Preserve it instead of falling back to NORMALIZING, which would
		// regenerate voiceovers and overlays before retrying the final render.
		if run.CurrentStage == StageCoreReady {
			return StagePublishingDocuments
		}
		// Resume from the failed stage (will retry).
		if run.FailedStage != "" && StageIndex(run.FailedStage) >= 0 {
			return run.FailedStage
		}
		return StageNormalizing
	case RunStatusRunning:
		// CORE_READY is a milestone, not a work phase (StageIndex returns -1
		// for it). The canonical result is already durable at this point, so
		// resuming from here must re-enter at the first phase that still has
		// work — the post-processing legs — instead of replaying the run.
		if run.CurrentStage == StageCoreReady {
			return StagePublishingDocuments
		}
		// Already running — resume from current stage.
		if run.CurrentStage != "" && StageIndex(run.CurrentStage) >= 0 {
			return run.CurrentStage
		}
		return StageNormalizing
	default:
		return StageNormalizing
	}
}
