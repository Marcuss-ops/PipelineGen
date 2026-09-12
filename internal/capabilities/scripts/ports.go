// Package scriptgeneration — ports.go defines the technology-independent
// interfaces for the script generation workflow. Every adapter
// (Ollama, Google Drive, TTS) implements one or more of these
// contracts.
//
// Verdetto invariant: no port returns a technology-specific type
// (e.g. *driveintegration.UploadResult). Every port returns only
// domain types from this package or standard library types.
//
// The document-publication ports live in ports_document.go and the
// render/execution ports in ports_execution.go (split 2026-09-12 to keep every
// file under max_lines_per_file_strict=600, godlike/08).
package scriptgeneration

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── TextGenerator ───────────────────────────────────────────────────

// TextGenerator produces the initial scene text (typically in English)
// from a generation request. It is the FIRST generative step in the
// workflow — the translator runs AFTER this step.
//
// Verdetto: a true TextGenerator must be introduced, separate from
// the Translator. The current code does NOT generate text — it only
// uses script_text already received or a local fallback.
type TextGenerator interface {
	// GenerateSceneText produces the scene text for the given request.
	// Returns the English (or primary-language) text for each scene.
	GenerateSceneText(ctx context.Context, request GenerateRequest) ([]Scene, error)
}

// SceneTextTraceGenerator is implemented by generators that resolve an
// auditable source (for example web research) while producing scene text.
// The trace must travel with the durable result; otherwise the async runner
// can successfully generate narration while silently dropping provenance.
type SceneTextTraceGenerator interface {
	TextGenerator
	GenerateSceneTextWithTrace(context.Context, GenerateRequest) ([]Scene, scriptpkg.SourceTrace, error)
}

// SceneTextStreamer is the optional streaming variant of TextGenerator. A
// generator that also implements this interface emits each scene as soon as
// its text becomes final, letting the runner fire SceneTextReady(N) (a
// SceneCommitted event) and start that scene's downstream branches while the
// LLM continues generating later scenes — instead of buffering the whole
// script behind one all-or-nothing return.
//
// Contract: emit must be called exactly once per scene, with immutable,
// already-final scene text (never partial tokens). Completion order may differ
// from scene order when bounded fan-out is enabled; Scene.Index is canonical
// and consumers must preserve it for final assembly. A non-nil error returned
// by emit aborts generation and fails the run. The runner falls back to the
// batch TextGenerator.GenerateSceneText when the generator does not implement
// this interface.
type SceneTextStreamer interface {
	GenerateSceneTextStream(ctx context.Context, request GenerateRequest, emit func(Scene) error) error
}

// SceneTextTraceStreamer preserves research provenance across the streaming
// boundary. It emits the same immutable scenes as SceneTextStreamer.
type SceneTextTraceStreamer interface {
	SceneTextStreamer
	GenerateSceneTextStreamWithTrace(context.Context, GenerateRequest, func(Scene) error) (scriptpkg.SourceTrace, error)
}

// ScriptPersistenceInput is the typed handoff to the canonical SQLite
// persistence adapter. The capability owns the decision to invoke it;
// the adapter owns storage details and idempotency.
type ScriptPersistenceInput struct {
	RunID   string
	Request GenerateRequest
	Result  *GenerateResult
}

// ScriptPersistence writes one canonical script row and returns its durable
// SQLite identifier. Implementations must be idempotent and must not silently
// succeed without returning a positive ID.
type ScriptPersistence interface {
	Persist(context.Context, ScriptPersistenceInput) (int64, error)
}

// ── Translator ──────────────────────────────────────────────────────

// TranslationInput carries the data needed to translate a single scene.
type TranslationInput struct {
	SceneID        string
	SourceLanguage Language
	TargetLanguage Language
	SourceText     string
}

// Translator translates scene text from one language to another.
// It is the SECOND generative step, running AFTER TextGenerator.
//
// Verdetto: must have explicit timeout, retry policy, typed errors
// (retryable vs non-retryable), and per-scene checkpoint persistence
// (so a retry resumes from the failed scene, not from scene 1).
type Translator interface {
	// Translate translates the input text to the target language.
	// Returns the translated text on success.
	Translate(ctx context.Context, input TranslationInput) (string, error)
}

// OverlayBackgroundAsset is the verified asset identity used to turn a
// caller's visual background asset_id into a content-addressed render input.
// LocalPath is a producer-side cache location and is never sent to the queue
// worker; URL/SHA256 are the durable identity used for remote staging.
type OverlayBackgroundAsset struct {
	AssetID   string
	LocalPath string
	URL       string
	SHA256    string
	MediaType string
}

// OverlayBackgroundSource resolves a visual background from the canonical
// asset catalog or local cache. It is deliberately separate from the audio
// asset port because image/video backgrounds have different media rules.
type OverlayBackgroundSource interface {
	ResolveOverlayBackground(context.Context, string) (OverlayBackgroundAsset, error)
}

// ── VoiceoverGenerator ──────────────────────────────────────────────

// VoiceoverInput carries the data needed to generate a voiceover.
type VoiceoverInput struct {
	SceneID  string
	Language Language
	Text     string
	// Project is the semantic project namespace for the voiceover publish
	// (PR-P12-VOICEOVER-SEMANTIC-FIELDS / PR-VOICEOVER-DRIVE-DRIFT). The
	// publisher REQUIRES a non-empty Project. The runner forwards
	// GenerateRequest.Project here — the value resolved ONCE by
	// BuildGenerateRequest — and fails the run BEFORE the first TTS call
	// when it is empty (ErrProjectRequired). No fallback namespace is
	// invented downstream.
	Project string
	// VoiceoverFolderID is the caller-explicit Drive folder for voiceover
	// artifacts (output.voiceover_folder_id), resolved ONCE by the routing
	// context. Empty means "use the configured default". The generator
	// forwards it verbatim into the per-item TTS command destination so a
	// caller-explicit folder is never replaced by the configured default.
	VoiceoverFolderID string
	// Timing is the canonical voiceover timing policy for this scene.
	// nil means the generator applies the canonical defaults
	// (best_effort / word / [json]) — timing capture is never implicitly
	// mandatory. When set, the generator forwards it to the per-item
	// pipeline so the required/best-effort fail-closed semantics are
	// honoured end-to-end.
	Timing *audio.TimingRequest
}

// VoiceoverGenerator produces audio assets from text.
// It is the THIRD generative step, running AFTER translation.
//
// Verdetto: must be truly generative (text → TTS → audio asset),
// not just a copier of existing voiceover_paths / audio_path fields.
type VoiceoverGenerator interface {
	// Generate produces a voiceover audio asset for the given text.
	Generate(ctx context.Context, input VoiceoverInput) (AudioReference, error)
}

// ── FailRunInput ────────────────────────────────────────────────────

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

// ── RunRepository ───────────────────────────────────────────────────

// RunRepository persists and retrieves GenerationRun aggregates.
// Used by the runner for checkpoint persistence after each stage.
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
	// Sets status=FAILED, updates failed_stage, error_code,
	// error_message, attempt_count, and next_retry_at.
	// Implementations persist whatever non-zero fields the input
	// carries; zero-valued optional fields are left unchanged.
	FailRun(ctx context.Context, input FailRunInput) error

	// SavePartialResult persists intermediate result data (e.g. after
	// each translated scene) so a retry can resume from the checkpoint.
	SavePartialResult(ctx context.Context, runID string, result *GenerateResult) error
}
