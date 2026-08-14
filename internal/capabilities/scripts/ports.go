// Package scriptgeneration — ports.go defines the technology-independent
// interfaces for the script generation workflow. Every adapter
// (Ollama, Google Drive, TTS) implements one or more of these
// contracts.
//
// Verdetto invariant: no port returns a technology-specific type
// (e.g. *driveintegration.UploadResult). Every port returns only
// domain types from this package or standard library types.
package scriptgeneration

import (
	"context"
	"time"

	"fmt"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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

// ── VoiceoverGenerator ──────────────────────────────────────────────

// VoiceoverInput carries the data needed to generate a voiceover.
type VoiceoverInput struct {
	SceneID  string
	Language Language
	Text     string
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

// ── DocumentPublisher ───────────────────────────────────────────────

// DocumentInput carries the data needed to publish a document.
type DocumentInput struct {
	RunID    string
	Language Language
	Title    string
	Content  string
	FolderID string
}

// DocumentPublisher publishes (creates or updates) a Google Doc.
//
// Verdetto: must be UPSERT, not CREATE — the identity is deterministic
// (generation_run_id + language). On retry the same document is updated
// rather than creating a duplicate.
type DocumentPublisher interface {
	// UpsertDocument creates a new document or updates an existing one
	// identified by (run_id, language) Drive properties.
	UpsertDocument(ctx context.Context, input DocumentInput) (DocumentReference, error)
}

// DocumentRenderOptions are the caller-facing inputs for the canonical
// document renderer. The renderer owns presentation; the runner only supplies
// the canonical model and the requested language.
type DocumentRenderOptions struct {
	Title           string
	Language        Language
	DefaultLanguage Language
	// FullAudio is the already-certified master produced by the audio
	// pipeline. The renderer only projects this reference; it never probes,
	// trims, mixes, or uploads the file.
	FullAudio *DocumentAudioRef
	// FinalAudio is the full certification of the master asset (codec,
	// profile, sample rate, channels, hashes, mix/copy eligibility). The
	// renderer projects it verbatim so the video renderer consumes exactly
	// the same asset the document certifies. Nil when no master was built.
	FinalAudio *FinalAudioReference
	// AudioTimeline is the canonical timeline used to compile FullAudio.
	AudioTimeline *audio.CanonicalTimeline
	// Overlay is the already-published reference to the completed render
	// overlay. It carries only the public artifact URL and copy-only
	// certification — never a local path — so the document and Velox copy
	// assembly reference the same immutable artifact. Nil when no render was
	// requested or the render has not produced an artifact.
	Overlay *DocumentOverlayRef
}

type DocumentAudioRef = scriptpkg.DocumentAudioRef

// DocumentOverlayRef is the published render-overlay reference projected into
// a document. See scriptpkg.DocumentOverlayRef for the field contract.
type DocumentOverlayRef = scriptpkg.DocumentOverlayRef

// DocumentRenderer is the single rendering seam used by every document
// producer. The composition root wires the canonical implementation, while
// the capability remains independent of HTML and Google Docs.
type DocumentRenderer interface {
	RenderDocument(*scriptpkg.ModelScriptOutputV1, DocumentRenderOptions) (string, error)
}

// IdentifiedDocumentRenderer is an optional observability seam. Production
// renderers implement it so completed runs prove which formatter was used;
// test renderers may omit it and remain valid port fakes.
type IdentifiedDocumentRenderer interface {
	DocumentRendererID() string
}

// ── RenderEnqueuer ──────────────────────────────────────────────────

// RenderEnqueuer submits a render job to the worker queue.
// It is the LAST step in the workflow.
type RenderEnqueuer interface {
	// Enqueue submits a render job and returns the job reference.
	Enqueue(ctx context.Context, result GenerateResult) (RenderReference, error)
}

// CombinedAudioRenderer is required only for COMBINED_TIMELINE jobs. It must
// return a probed, certified final audio artifact; the runner never falls
// back to chunked mixing when this port is unavailable or fails.
type CombinedAudioRenderer interface {
	Render(ctx context.Context, plan audio.CompiledAudioPlan, assets audio.ResolvedAudioAssets) (FinalAudioReference, AudioPipelineMetrics, error)
}

// FinalAudioPublisher publishes the already-certified combined master before
// document publication. It is deliberately narrower than the renderer: it
// receives the immutable result and returns only the canonical public link.
type FinalAudioPublisher interface {
	PublishFinalAudio(context.Context, string, Language, FinalAudioReference) (string, error)
}

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
}

// noopExecutionRecorder keeps local/unit runtimes safe when durable lineage
// is intentionally not wired. Production composition injects the Job Registry
// adapter; this is not a fake success path for registry writes.
type noopExecutionRecorder struct{}

func (noopExecutionRecorder) StartStep(context.Context, ExecutionContext, ExecutionStep) error {
	return nil
}
func (noopExecutionRecorder) CompleteStep(context.Context, ExecutionContext, ExecutionStep) error {
	return nil
}
func (noopExecutionRecorder) FailStep(context.Context, ExecutionContext, ExecutionStep, error) error {
	return nil
}
func (noopExecutionRecorder) AttachInputAsset(context.Context, ExecutionContext, string, string, int) error {
	return nil
}
func (noopExecutionRecorder) AttachOutputAsset(context.Context, ExecutionContext, string, string, int) error {
	return nil
}
func (noopExecutionRecorder) RecordMetric(context.Context, ExecutionContext, string, string, float64, string) error {
	return nil
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
