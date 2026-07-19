// Package scriptgeneration — ports.go defines the technology-independent
// interfaces for the script generation workflow. Every adapter
// (Ollama, Google Drive, TTS, Remotion) implements one or more of
// these contracts.
//
// Verdetto invariant: no port returns a technology-specific type
// (e.g. *driveintegration.UploadResult). Every port returns only
// domain types from this package or standard library types.
package scriptgeneration

import "context"

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

// ── RenderEnqueuer ──────────────────────────────────────────────────

// RenderEnqueuer submits a render job to the worker queue.
// It is the LAST step in the workflow.
type RenderEnqueuer interface {
	// Enqueue submits a render job and returns the job reference.
	Enqueue(ctx context.Context, result GenerateResult) (RenderReference, error)
}

// ── RunRepository ───────────────────────────────────────────────────

// RunRepository persists and retrieves GenerationRun aggregates.
// Used by the runner for checkpoint persistence after each stage.
type RunRepository interface {
	// Create persists a new GenerationRun.
	Create(ctx context.Context, run *GenerationRun) error

	// Get retrieves a GenerationRun by ID.
	Get(ctx context.Context, runID string) (*GenerationRun, error)

	// UpdateStage persists the current stage and status atomically.
	// Implementations should only UPDATE the stage-relevant columns
	// (current_stage, status, updated_at) — not the full run.
	UpdateStage(ctx context.Context, runID string, status RunStatus, stage Stage) error

	// SavePartialResult persists intermediate result data (e.g. after
	// each translated scene) so a retry can resume from the checkpoint.
	SavePartialResult(ctx context.Context, runID string, result *GenerateResult) error
}
