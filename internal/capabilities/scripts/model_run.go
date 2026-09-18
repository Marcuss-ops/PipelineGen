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
	StagePublishingDocuments  Stage = "PUBLISHING_DOCUMENTS"
	StageCompleted            Stage = "COMPLETED"
	StageFailed               Stage = "FAILED"
	// StageCoreReady is the CORE_READY milestone: the certified render, the
	// published final audio and the canonical script result are durable, and
	// only the post-processing legs remain (Google Docs publication and the
	// artifact/Drive finalization the worker performs after this run returns).
	//
	// It is deliberately NOT a member of stageOrder: it is a milestone with no
	// work of its own, so StageIndex returns -1 for it exactly like the
	// terminal stages. ResumeFrom maps it explicitly onto the first phase that
	// still has work (StagePublishingDocuments) instead of restarting the run.
	//
	// It is also NOT a terminal state. SUCCEEDED/COMPLETED keeps its existing
	// meaning — every requested artifact is published — so observing
	// CORE_READY can never be mistaken for "the job is done".
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
// per-scene entity extraction via output.extract_entities=disabled. The
// incremental VidRush pipeline (entity extraction → provider fan-out) is
// skipped for such runs; ToggleDefault and ToggleEnabled preserve the
// canonical always-extract behavior.
func (req GenerateRequest) EntityExtractionDisabled() bool {
	return req.ExtractEntities == scriptpkg.ToggleDisabled
}

// NeedsSemanticEnrichment reports whether the run has a real downstream
// consumer for per-scene semantic extraction. Omitted entity extraction is
// intentionally no longer an implicit NLP request: plain text/audio/docs
// runs do not need VidRush, while explicit entities, scene images, enabled
// extraction policy, or an active visual media plan do.
func (req GenerateRequest) NeedsSemanticEnrichment() bool {
	if req.ExtractEntities == scriptpkg.ToggleDisabled {
		return false
	}
	if req.ExtractEntities == scriptpkg.ToggleEnabled || req.GenerateSceneImages == scriptpkg.ToggleEnabled {
		return true
	}
	if req.MediaPlan.Extraction.Enabled || req.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
		return true
	}
	return mediadomain.IsActiveMediaPlanMode(req.MediaPlan.Mode)
}

// ResolveDocsConfig resolves the effective document publishing config
// from a GenerateRequest, applying backward-compat fallback from the
// deprecated flat fields (DocsEnabled, DriveFolderID) to the canonical
// DocumentsConfig struct (Docs).
//
// Returns (enabled, languages, folderID):
//   - enabled: true when Docs.Enabled || DocsEnabled
//   - languages: Docs.Languages, falling back to top-level Languages
//   - folderID: Docs.FolderID, falling back to DriveFolderID
//
// Once all callers migrate to the Docs struct, this helper can be
// removed and callers can read req.Docs directly.
func (req GenerateRequest) ResolveDocsConfig() (enabled bool, languages []Language, folderID string) {
	enabled = req.Docs.Enabled || req.DocsEnabled
	languages = req.Docs.Languages
	if len(languages) == 0 && enabled {
		languages = req.Languages
	}
	folderID = req.Docs.FolderID
	if folderID == "" {
		folderID = req.DriveFolderID
	}
	return
}

// IsTerminal returns true when the stage signals a terminal state.
func (s Stage) IsTerminal() bool {
	return s == StageCompleted || s == StageFailed
}

// ── Completion contract ─────────────────────────────────────────────

// IsCoreCompletable checks whether the CORE half of a run is durable.
//
// CORE contract (the part that must hold at the CORE_READY boundary):
//
//	scene normalizzate > 0
//	ogni scena ha testo per ogni lingua richiesta
//
// It deliberately says NOTHING about Google Docs. The render, the certified
// final audio and the script row are already durable by the time this is
// evaluated — they are produced by the phases that precede the boundary
// (audioCompile → persist) and are not re-derived here. What this predicate
// answers is the question the boundary actually asks: "is the canonical
// scene/text core present, so that the only thing left is post-processing?"
//
// NO-FAKE-AVAILABILITY: a caller must never advertise core readiness when
// this returns false, so a run whose text is incomplete cannot look ready
// while Docs/Drive are still pending.
func IsCoreCompletable(result *GenerateResult, wantedLanguages []Language) bool {
	if result == nil {
		return false
	}
	if len(result.Scenes) == 0 {
		return false
	}

	// Every scene must have text for every wanted language.
	for _, scene := range result.Scenes {
		for _, lang := range wantedLanguages {
			if scene.Text[lang] == "" {
				return false
			}
		}
	}

	return true
}

// IsRunCompletable checks whether a run has satisfied all preconditions for
// transitioning to COMPLETED. It is the CORE contract PLUS every requested
// document, so the terminal state keeps its existing meaning: a run is only
// COMPLETED once the post-processing legs have actually produced their
// artifacts. The split exists so the interim CORE_READY boundary can be
// asserted honestly (IsCoreCompletable) without weakening COMPLETED.
//
// Verdetto contract:
//
//	scene normalizzate > 0
//	ogni scena ha testo per ogni lingua richiesta
//	documento per ogni lingua richiesta ha id e link
//	nessuna fase richiesta è PENDING
func IsRunCompletable(result *GenerateResult, wantedLanguages []Language) bool {
	if !IsCoreCompletable(result, wantedLanguages) {
		return false
	}

	// Every wanted language must have a published document with ID and link.
	for _, lang := range wantedLanguages {
		doc, ok := result.Documents[lang]
		if !ok || doc.ID == "" || doc.Link == "" {
			return false
		}
	}

	return true
}

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
