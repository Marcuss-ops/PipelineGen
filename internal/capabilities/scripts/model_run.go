// model_run.go — durable run aggregate: GenerationRun, run/retry
// state machine and error sentinels (split from model.go).
package scriptgeneration

import (
	"errors"
	"time"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type GenerationRun struct {
	// ID is the canonical run identifier (pipeline_run.id).
	ID string `json:"id"`

	// JobID is the canonical worker-assigned job identifier, set
	// after the submission service returns. Used by GET /full to
	// correlate the job with its generation run.
	JobID string `json:"job_id,omitempty"`

	// Request is the original generation request.
	Request GenerateRequest `json:"request"`

	// Status is the high-level lifecycle status.
	Status RunStatus `json:"status"`

	// CurrentStage identifies the exact workflow phase.
	CurrentStage Stage `json:"current_stage"`

	// Result is populated when the run reaches a terminal state.
	Result *GenerateResult `json:"result,omitempty"`

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

// RunStatus is the high-level lifecycle status of a GenerationRun.
type RunStatus string

const (
	RunStatusPending   RunStatus = "PENDING"
	RunStatusRunning   RunStatus = "RUNNING"
	RunStatusCompleted RunStatus = "COMPLETED"
	RunStatusFailed    RunStatus = "FAILED"
)

// Stage identifies the exact phase within the workflow.
// Verdetto: extend CurrentStage with these explicit values.
type Stage string

const (
	StageNormalizing          Stage = "NORMALIZING"
	StagePreflight            Stage = "MEDIA_PREFLIGHT" // P0.5: fail-fast asset verification before GENERATING_SCENE_TEXT
	StageGeneratingSceneText  Stage = "GENERATING_SCENE_TEXT"
	StageTranslatingScenes    Stage = "TRANSLATING_SCENES"
	StageGeneratingVoiceovers Stage = "GENERATING_VOICEOVERS"
	StageCompilingAudio       Stage = "COMPILING_AUDIO"
	// StagePublishingDocuments is the LEGACY publication stage. Document
	// publication was removed with Google Docs; the stage value survives for
	// persisted-run resume compatibility and as the failure stage the remote
	// final-job/artifact publication path reports.
	StagePublishingDocuments Stage = "PUBLISHING_DOCUMENTS"
	StageCompleted           Stage = "COMPLETED"
	StageFailed              Stage = "FAILED"
	// StageCoreReady is the LEGACY CORE_READY milestone, kept only so a run
	// persisted before the Google Docs demolition still resumes at the
	// post-core publication stage instead of replaying from NORMALIZING.
	// Nothing writes it any more.
	StageCoreReady Stage = "CORE_READY"
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

// EntityExtractionDisabled reports whether the caller explicitly disabled
// run-level entity extraction via output.extract_entities=disabled. A segment
// with extract_entities=enabled may still opt back in independently.
func (req GenerateRequest) EntityExtractionDisabled() bool {
	return req.ExtractEntities == scriptpkg.ToggleDisabled
}

// HasEnabledSegmentEntityExtraction reports whether at least one segment
// explicitly opts back into entity extraction when the run-level toggle is off.
func (req GenerateRequest) HasEnabledSegmentEntityExtraction() bool {
	for _, segment := range req.ScriptParams.Segments {
		if segmentExtractionExplicitlyEnabled(segment) {
			return true
		}
	}
	return false
}

// NeedsSemanticEnrichment reports whether the run has a real downstream
// consumer for per-scene semantic extraction. Omitted entity extraction is
// intentionally no longer an implicit NLP request: plain text/audio/docs
// runs do not need VidRush, while explicit entities, scene images, enabled
// extraction policy, or an active visual media plan do.
func (req GenerateRequest) NeedsSemanticEnrichment() bool {
	if req.ExtractEntities == scriptpkg.ToggleDisabled && !req.HasEnabledSegmentEntityExtraction() {
		return false
	}
	if req.HasEnabledSegmentEntityExtraction() || req.ExtractEntities == scriptpkg.ToggleEnabled || req.GenerateSceneImages == scriptpkg.ToggleEnabled {
		return true
	}
	if req.MediaPlan.Extraction.EntityImageSurfaceEnabled() ||
		(req.MediaPlan.Extraction.Enabled && !req.MediaPlan.Extraction.HasCategoryOnlyIncludes()) ||
		req.MediaPlan.Extraction.EntityExtractionExplicitlyRequested() {
		return true
	}
	return mediadomain.IsActiveMediaPlanMode(req.MediaPlan.Mode)
}

// ErrGoogleDocsRetired is the typed sentinel for a generate request that still
// asks for Google Docs publication. The capability — document rendering, the
// Google Docs client, the deferred `script.docs_publish` child job and the
// localization document assembler — was removed completely. Keeping the
// request fail-closed means a caller never believes a document was produced
// when it was not (godlike/07 NO-FAKE-AVAILABILITY).
var ErrGoogleDocsRetired = errors.New("scriptgeneration: Google Docs publication has been retired; omit the docs block or set docs.enabled=false")

// DocsRequested reports whether the request still asks for the retired Google
// Docs capability through the canonical (docs.enabled) or legacy
// (docs_enabled) field.
func (req GenerateRequest) DocsRequested() bool {
	return req.Docs.Enabled || req.DocsEnabled
}

// IsTerminal returns true when the stage signals a terminal state.
func (s Stage) IsTerminal() bool {
	return s == StageCompleted || s == StageFailed
}

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
var stageOrder = []Stage{
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
	for i, st := range stageOrder {
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
