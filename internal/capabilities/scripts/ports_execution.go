// Package scriptgeneration — ports_execution.go: the render/overlay enqueuer
// ports and the execution-recorder contract.
//
// These are the seams the durable runner uses to enqueue follow-up render
// work (localized renders, overlay plans) and to record per-step execution
// evidence. Every port returns domain types only (verdetto invariant,
// ports.go).
//
// Extracted 2026-09-12 from ports.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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
	// The runner records each produced localized clip (asset id, sha256,
	// Drive link) on the run result. A non-nil sink is fail-closed: a sink
	// error fails the enqueue. Nil means the caller does not need the outcome.
	OnRendered func(LocalizedRenderResult) error `json:"-"`
	// OnRenderReady is invoked after local render certification and before
	// upload, allowing durable recovery to retry Drive without rerendering.
	OnRenderReady func(LocalizedRenderResult) error `json:"-"`
	// ResumeFrom, when set, identifies a locally certified RENDERED artifact
	// restored from a partial result. The adapter may publish it directly.
	ResumeFrom *LocalizedRenderResult             `json:"-"`
	OnFailed   func(LocalizedRenderFailure) error `json:"-"`
}

// LocalizedRenderResult is the certified outcome of one localized render
// fan-out: the produced video artifact identity (asset id, sha256, Drive
// link, duration) for the (scene, language) unit. It is projected verbatim
// from the localization service's certified artifact — the run records the
// produced MP4 instead of discarding it.
type LocalizedRenderResult struct {
	SceneID     string             `json:"scene_id"`
	SceneIndex  int                `json:"scene_index,omitempty"`
	Language    Language           `json:"language"`
	ClipID      string             `json:"clip_id"`
	AssetID     string             `json:"asset_id"`
	SHA256      string             `json:"sha256"`
	DriveFileID string             `json:"drive_file_id,omitempty"`
	DriveLink   string             `json:"drive_link,omitempty"`
	DurationMS  int64              `json:"duration_ms,omitempty"`
	LocalPath   string             `json:"local_path,omitempty"`
	Status      string             `json:"status"`
	Backend     string             `json:"backend,omitempty"`
	Metrics     map[string]float64 `json:"metrics,omitempty"`
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

// LocalizedRenderRecoveryEnqueuer is optional for backwards-compatible
// adapters. It publishes a staged local artifact without rerendering it.
type LocalizedRenderRecoveryEnqueuer interface {
	UploadRendered(context.Context, LocalizedRenderInput, LocalizedRenderResult) error
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
// port (P0.5). When wired, the runner executes it synchronously after
// normalization and before scene-text generation. Any failure aborts the run
// before LLM, translation, or TTS. Nil is tolerated for requests without
// fixed media; fixed-media requests fail closed when no preflight is wired.
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
