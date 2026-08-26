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
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"time"

	"fmt"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
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

// DocumentPublisherPreflight is implemented only by real provider-backed
// publishers. A requested Google Doc must be validated before generation;
// test/no-op publishers intentionally do not satisfy this interface.
type DocumentPublisherPreflight interface {
	DocumentPublisher
	Preflight(context.Context, string) error
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
	// SceneSpeechTimings is the deterministic scene-level speech timing
	// projection (scene word boundaries + phrase spans in local and global
	// coordinates). The renderer projects it into the human surface and the
	// machine JSON section; it never derives or invents timestamps itself.
	SceneSpeechTimings []audio.SceneSpeechTiming
	// ClipMetadata is the canonical, pre-resolved clip-asset metadata
	// (total source duration in integer microseconds). The renderer formats
	// it verbatim; it never converts or derives clip durations.
	ClipMetadata []audio.ClipAssetMetadata
	// AudioSummary is the pre-computed aggregate of the audio facts (clip
	// totals, voiceover totals, counts) resolved at the capability boundary.
	// The renderer only formats it; it never sums durations across scenes.
	AudioSummary audio.DocumentAudioSummary
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

// SplittableDocumentRenderer is the optional early/late rendering seam. When
// a renderer implements it, the runner renders the scene-text-only skeleton
// at SceneTextReady (so the CPU half of DocsPrepare overlaps TTS and NLP) and
// then fills the late-bound markers after the audio join. Renderers that only
// implement DocumentRenderer keep the one-shot path; the two paths must be
// byte-equivalent for a non-nil model.
type SplittableDocumentRenderer interface {
	RenderDocumentSkeleton(DocumentSkeletonInput) string
	InjectDocumentLateBound(skeleton string, model *scriptpkg.ModelScriptOutputV1, opts DocumentRenderOptions) string
}

// IdentifiedDocumentRenderer is an optional observability seam. Production
// renderers implement it so completed runs prove which formatter was used;
// test renderers may omit it and remain valid port fakes.
type IdentifiedDocumentRenderer interface {
	DocumentRendererID() string
}

// ── RenderEnqueuer removed ───────────────────────────────────────
// The video render enqueue port was removed with the video render path.
// The future Chronon overlay path submits through
// QueueRenderEnqueuer.EnqueueChrononPlan, which does not need this port.

// OverlayPrepareEnqueuer enqueues the overlay.prepare job for the run's
// pre-timing OverlayIntents. It is fire-and-forget: prepare resolves
// templates and prefetches entity assets independently of the timing-frozen
// render path and must never block the pipeline. The runner treats a nil
// enqueuer as "prepare not registered" (a legitimate no-op for environments
// without a RenderingGen queue) and a non-nil enqueuer as fail-closed (an
// enqueue error fails the run — never a silent no-op).
type OverlayPrepareEnqueuer interface {
	EnqueuePrepare(context.Context, capabilityoverlay.PrepareRequest) error
}

// OverlayRenderEnqueuer submits the timing-frozen OverlayPlan to
// RenderingGen and returns the certified Chronon artifact. Prepare is
// fire-and-forget; render is allowed only after canonical timing exists.
type OverlayRenderEnqueuer interface {
	EnqueueChrononPlan(context.Context, capabilityoverlay.OverlayPlan) (RenderReference, error)
}

// LocalizedRenderInput is one ready-to-render localized unit: a scene whose
// translation and voiceover for one language are final. It is the per-(scene,
// language) trigger of the localized render fan-out — the runner emits it the
// moment that unit's TTS completes, never after a global join.
type LocalizedRenderInput struct {
	RunID       string
	ParentJobID string `json:"parent_job_id,omitempty"`
	SceneID     string
	SceneIndex  int
	Language    Language
	Text        string
	Voiceover   AudioReference

	// SourceLanguage / SourceText carry the source-language scene text (the
	// transcript the localized render's source plan references). They are
	// populated for every language so the enqueuer can persist the source
	// transcript track alongside the translated subtitle track without
	// re-deriving them; SourceText is empty only for a scene whose source
	// text was never produced.
	SourceLanguage Language `json:"source_language,omitempty"`
	SourceText     string   `json:"source_text,omitempty"`

	// ClipID / ClipAssetID / ClipSHA256 / ClipDurationMS carry the source
	// clip the localized render burns subtitles onto. They are empty for
	// audio-only scenes (no source clip); the enqueuer decides whether an
	// empty reference is a no-op or a fail-closed error.
	ClipID         string `json:"clip_id,omitempty"`
	ClipAssetID    string `json:"clip_asset_id,omitempty"`
	ClipSHA256     string `json:"clip_sha256,omitempty"`
	ClipDurationMS int64  `json:"clip_duration_ms,omitempty"`

	// Render carries the caller's explicit watermark/subtitle request. The
	// adapter resolves the referenced watermark asset and passes the sealed
	// choices to the single Rust clip-render pass.
	Render scriptpkg.VideoRenderSpec `json:"render,omitempty"`

	// OnRendered, when non-nil, is invoked once per certified produced video
	// of the fan-out (render + upload completed) with that video's identity.
	// The runner uses it to record the final video (asset id, sha256, Drive
	// link) on the run result so the produced MP4 is never orphaned from the
	// run that produced it — a run that rendered a video must be able to
	// prove it. A non-nil sink is fail-closed: a sink error fails the
	// enqueue. Nil means the caller does not need the outcome.
	OnRendered func(LocalizedRenderResult) error  `json:"-"`
	OnFailed   func(LocalizedRenderFailure) error `json:"-"`
}

// LocalizedRenderResult is the certified outcome of one localized render
// fan-out: the produced video artifact identity (asset id, sha256, Drive
// link, duration) for the (scene, language) unit. It is projected verbatim
// from the localization service's certified artifact — the run records the
// produced MP4 instead of discarding it.
type LocalizedRenderResult struct {
	SceneID     string   `json:"scene_id"`
	SceneIndex  int      `json:"scene_index,omitempty"`
	Language    Language `json:"language"`
	ClipID      string   `json:"clip_id"`
	AssetID     string   `json:"asset_id"`
	SHA256      string   `json:"sha256"`
	DriveFileID string   `json:"drive_file_id,omitempty"`
	DriveLink   string   `json:"drive_link,omitempty"`
	DurationMS  int64    `json:"duration_ms,omitempty"`
	LocalPath   string   `json:"local_path,omitempty"`
	Status      string   `json:"status"`
	// Boundary timestamps let the parent distinguish summed child work from
	// actual fan-out wall time when localized renders overlap.
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	WallMS     int64     `json:"wall_ms,omitempty"`
}

type LocalizedRenderFailure struct {
	SceneID   string   `json:"scene_id"`
	Language  Language `json:"language"`
	ClipID    string   `json:"clip_id,omitempty"`
	ErrorCode string   `json:"error_code"`
	Error     string   `json:"error"`
}

// LocalizedRenderEnqueuer enqueues one localized render as soon as a scene's
// translation + TTS for a language are ready, so Rust can start on scene 1 ES
// while scene 2 is still being translated/voiced. A nil enqueuer is a
// legitimate no-op (render not registered); a non-nil enqueuer is fail-closed
// (an enqueue error fails the run, never a silent skip).
type LocalizedRenderEnqueuer interface {
	EnqueueLocalizedRender(context.Context, LocalizedRenderInput) error
}

// CombinedAudioRenderer is required only for COMBINED_TIMELINE jobs. It must
// return a probed, certified final audio artifact; the runner never falls
// back to chunked mixing when this port is unavailable or fails.
type CombinedAudioRenderer interface {
	Render(ctx context.Context, plan audio.CompiledAudioPlan, assets audio.ResolvedAudioAssets) (FinalAudioReference, AudioPipelineMetrics, error)
}

// AudioAssetSource resolves ONE background-music or sound-effect asset_id
// to its canonical ResolvedAudioAsset (verified local path + certified
// source duration). The concrete adapter (composition root) reads the
// canonical asset registry and materializes Drive sources into scratch;
// the capability never imports Drive, SQLite, or the filesystem mechanics.
// Fail-closed: an unknown asset_id or an asset with no usable local copy is
// a typed error, never a silent empty path.
type AudioAssetSource interface {
	ResolveAudioAsset(ctx context.Context, assetID string) (audio.ResolvedAudioAsset, error)
}

// ClipAudioAssetSource is the optional production resolver for original clip
// audio. It is deliberately separate from AudioAssetSource because BGM/SFX
// resolution and clip-source materialization have different contracts.
type ClipAudioAssetSource interface {
	ResolveClipAudioAsset(ctx context.Context, assetID string) (audio.ResolvedAudioAsset, error)
}

// MediaPreflight is the optional fail-fast media requirement verification
// port (P0.5). When wired, the runner fires it in a goroutine in parallel
// with Gemma; after scene text generation completes, the runner joins the
// preflight and fails the run if any check failed. Nil means the preflight
// is skipped (backward compat for tests and deployments without wired
// resolvers).
type MediaPreflight interface {
	Run(ctx context.Context, req GenerateRequest) PreflightResult
}

// FinalAudioPublishResult is the canonical publication outcome: the canonical
// MediaRegistry asset ID plus the public Drive link. It never carries a local
// filesystem path.
type FinalAudioPublishResult struct {
	AssetID   string `json:"audio_asset_id"`
	DriveLink string `json:"drive_link"`
}

// FinalAudioPublisher publishes the already-certified combined master before
// document publication and registers it as a canonical MediaRegistry asset.
// It returns the canonical asset ID (never a local path) plus the public
// Drive link. voiceoverFolderID is the caller-explicit Drive folder
// (output.voiceover_folder_id); empty means "use the configured default".
type FinalAudioPublisher interface {
	PublishFinalAudio(context.Context, string, Language, FinalAudioReference, string) (FinalAudioPublishResult, error)
}

// FinalVideoAssembler concatenates the certified localized clip files in
// scene order. It must fail when any input is missing and return the actual
// output path only after the file has been created.
type FinalVideoAssembler interface {
	AssembleFinalVideo(context.Context, []string, string) error
}

type FinalVideoPublisher interface {
	PublishFinalVideo(context.Context, string, FinalVideoReference, string) (FinalAudioPublishResult, error)
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
	Language Language `json:"language,omitempty"`
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
func (noopExecutionRecorder) RecordOperation(context.Context, ExecutionContext, ArtifactOperation) error {
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
