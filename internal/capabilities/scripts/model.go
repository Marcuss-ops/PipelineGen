// Package scriptgeneration defines the pure domain model for the
// durable multi-stage script-generation workflow. It contains
// ZERO I/O, ZERO external dependencies — only types and pure
// functions.
//
// Architecture (per the verdetto):
//
//	internal/scriptgeneration/
//	    model.go      — GenerateRequest, Scene, GenerateResult and value types
//	    ports.go      — Technology-independent interfaces
//	    service.go    — Linear orchestrator (Start)
//	    runner.go     — Durable stage-based execution with checkpoint
//
// The HTTP layer (internal/api/script) calls service.Start; the
// runner owns the durable background execution. No I/O happens
// inside the builder (ingress registry) — the builder is demoted
// to pure payload transformation.
package scriptgeneration

import "time"

import capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
import capabilityrender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

// ── Value types ─────────────────────────────────────────────────────

// Language is an ISO 639-1 two-letter code (e.g. "en", "es").
type Language string

// Source describes where the generation input comes from.
type Source struct {
	Type         SourceType `json:"type"`
	Topic        string     `json:"topic,omitempty"`
	SourceText   string     `json:"source_text,omitempty"`
	ClipIDs      []string   `json:"clip_ids,omitempty"`
	IntroClipIDs []string   `json:"intro_clip_ids,omitempty"`
	NumClips     int        `json:"num_clips,omitempty"`
	Query        string     `json:"query,omitempty"`
	MaxClips     int        `json:"max_clips,omitempty"`
}

// SourceType enumerates the supported generation sources.
type SourceType string

const (
	SourceText    SourceType = "text"
	SourceClips   SourceType = "clips"
	SourceCatalog SourceType = "catalog"
	SourceSearch  SourceType = "search"
	SourceCurate  SourceType = "curate"
)

// ClipReference identifies a single media clip.
type ClipReference struct {
	ID           string  `json:"id"`
	SourceID     string  `json:"source_id,omitempty"`
	Title        string  `json:"title,omitempty"`
	DriveLink    string  `json:"drive_link,omitempty"`
	Duration     float64 `json:"duration,omitempty"` // seconds
	AudioAssetID string  `json:"audio_asset_id,omitempty"`
	AudioPath    string  `json:"audio_path,omitempty"`
	Path         string  `json:"path,omitempty"`
	SHA256       string  `json:"sha256,omitempty"`
	FrameCount   int64   `json:"frame_count,omitempty"`
	SourceInMS   int64   `json:"source_in_ms,omitempty"`
	SourceOutMS  int64   `json:"source_out_ms,omitempty"`
}

// AudioReference identifies a generated voiceover audio asset.
type AudioReference struct {
	ID       string  `json:"id"`
	URL      string  `json:"url,omitempty"`
	FilePath string  `json:"file_path,omitempty"`
	Duration float64 `json:"duration,omitempty"` // seconds
}

// DocumentReference identifies a published Google Doc.
type DocumentReference struct {
	ID   string `json:"id"`
	Link string `json:"link"`
}

// RenderReference identifies an enqueued render job and, once the render
// completes, carries the certified artifact the downstream document/assembly
// steps consume. The Artifact field is nil while the job is still queued or
// running.
type RenderReference struct {
	JobID    string          `json:"job_id"`
	Status   string          `json:"status"`
	Artifact *RenderArtifact `json:"artifact,omitempty"`
}

// RenderArtifact is the certified artifact produced by the central
// RenderingGen queue. It mirrors the queue's artifact contract (including the
// copy-only certification) so the document renderer and Velox copy assembly
// consume the same immutable reference without probing the file themselves.
type RenderArtifact struct {
	ID                 string `json:"id,omitempty"`
	Kind               string `json:"kind,omitempty"`
	StorageKey         string `json:"storage_key,omitempty"`
	URL                string `json:"url,omitempty"`
	SHA256             string `json:"sha256,omitempty"`
	MimeType           string `json:"mime_type,omitempty"`
	SizeBytes          int64  `json:"size_bytes,omitempty"`
	Width              int    `json:"width,omitempty"`
	Height             int    `json:"height,omitempty"`
	FPSNum             int    `json:"fps_num,omitempty"`
	FPSDen             int    `json:"fps_den,omitempty"`
	FrameCount         int    `json:"frame_count,omitempty"`
	DurationUS         int64  `json:"duration_us,omitempty"`
	ProfileID          string `json:"profile_id,omitempty"`
	CopyEligible       bool   `json:"copy_eligible,omitempty"`
	Codec              string `json:"codec,omitempty"`
	CodecProfile       string `json:"codec_profile,omitempty"`
	ClosedGOP          bool   `json:"closed_gop,omitempty"`
	FirstFrameKeyframe bool   `json:"first_frame_keyframe,omitempty"`
}

type FinalAudioReference struct {
	AssetID              string `json:"audio_asset_id"`
	Path                 string `json:"path,omitempty"`
	DriveLink            string `json:"drive_link,omitempty"`
	Container            string `json:"container,omitempty"`
	AudioContractVersion string `json:"audio_contract_version,omitempty"`
	AudioPlanVersion     string `json:"audio_plan_version,omitempty"`
	PlanSHA256           string `json:"audio_plan_sha256"`
	FinalAudioSHA256     string `json:"final_audio_sha256"`
	Codec                string `json:"codec,omitempty"`
	Profile              string `json:"profile,omitempty"`
	SampleRate           int    `json:"sample_rate,omitempty"`
	Channels             int    `json:"channels,omitempty"`
	ChannelLayout        string `json:"channel_layout,omitempty"`
	Bitrate              int64  `json:"bitrate,omitempty"`
	DurationUS           int64  `json:"duration_us,omitempty"`
	DurationMS           int64  `json:"duration_ms"`
	StartPTS             int64  `json:"start_pts,omitempty"`
	SizeBytes            int64  `json:"size_bytes,omitempty"`
	FinalMix             bool   `json:"final_mix,omitempty"`
	CopyEligible         bool   `json:"copy_eligible"`
}

// AudioPipelineMetrics is the canonical durable timing contract for the
// combined-audio pipeline (COMBINED_TIMELINE). It is owned by this capability
// package and is the surface the script runner persists under
// GenerateResult.AudioMetrics (JSON "audio_metrics").
//
// Relationship to the legacy domain contract: internal/domain/script
// .GenerationTimings carries an overlapping set of flat *_ms audio fields used
// only by the migration-only internal/application/scripts/usecase path. That
// struct is a legacy projection; this struct is the authority. Field map:
//
//	GenerationTimings (domain, legacy)  AudioPipelineMetrics (canonical)
//	tts_total_ms                          tts_ms
//	audio_mix_ms                          mix_ms
//	audio_encode_ms                       aac_encode_ms
//	audio_probe_ms                        probe_ms
//	audio_hash_ms                         hash_ms
//	audio_pipeline_total_ms               total_ms
//	final_audio_duration_ms               audio_duration_ms
//	audio_encode_passes                   audio_encode_passes (unchanged)
//	tts_calls                             tts_calls (unchanged)
//	timeline_compile_ms                   timeline_compile_ms (unchanged)
//	audio_plan_compile_ms                 audio_plan_compile_ms (unchanged)
//	clip_audio_prepare_ms                 clip_audio_prepare_ms (unchanged)
//
// Canonical-only fields with no legacy equivalent: media_fetch_ms, upload_ms,
// audio_rtf, audio_speed, tts_scenes.
//
// Do NOT add new audio timing fields to GenerationTimings; extend this struct
// instead. The legacy struct is migration-only and must converge here.
type AudioPipelineMetrics struct {
	TTSMS              int64             `json:"tts_ms"`
	MediaFetchMS       int64             `json:"media_fetch_ms"`
	TimelineCompileMS  int64             `json:"timeline_compile_ms"`
	AudioPlanCompileMS int64             `json:"audio_plan_compile_ms"`
	ClipAudioPrepareMS int64             `json:"clip_audio_prepare_ms"`
	MixMS              int64             `json:"mix_ms"`
	AACEncodeMS        int64             `json:"aac_encode_ms"`
	ProbeMS            int64             `json:"probe_ms"`
	HashMS             int64             `json:"hash_ms"`
	UploadMS           int64             `json:"upload_ms"`
	TotalMS            int64             `json:"total_ms"`
	AudioDurationMS    int64             `json:"audio_duration_ms"`
	TTSCalls           int               `json:"tts_calls"`
	AudioRTF           float64           `json:"audio_rtf"`
	AudioSpeed         float64           `json:"audio_speed"`
	AudioEncodePasses  int               `json:"audio_encode_passes"`
	TTSScenes          []TTSSSceneMetric `json:"tts_scenes,omitempty"`
}

type TTSSSceneMetric struct {
	SceneID          string   `json:"scene_id"`
	Language         Language `json:"language"`
	DurationMS       int64    `json:"duration_ms"`
	Characters       int      `json:"characters"`
	Words            int      `json:"words"`
	OutputDurationMS int64    `json:"output_duration_ms,omitempty"`
}

// ── DocumentsConfig ──────────────────────────────────────────────────

// DocumentsConfig is the explicit contract for Google Doc publishing.
// Per the verdetto, document creation MUST NOT be implicit based on
// whether drive_output_folder happens to be present — it must be
// explicitly requested via this config.
//
// One document per language is created (e.g. "script_en", "script_es").
// The identity is deterministic: (generation_run_id + language) drive
// properties. On retry, UpsertDocument updates the same document.
type DocumentsConfig struct {
	// Enabled explicitly requests Google Doc publishing.
	// When false, no documents are created even if Languages or
	// FolderID are populated. Default false (opt-in).
	Enabled bool `json:"enabled"`

	// Languages lists the languages for which a document should be
	// published. Each language gets its own document (one per language,
	// NOT one bilingual document). Must be non-empty when Enabled is
	// true.
	Languages []Language `json:"languages,omitempty"`

	// FolderID is the target Google Drive folder ID for documents.
	// When empty, documents are created in the default Drive location.
	FolderID string `json:"folder_id,omitempty"`
}

// ── Domain aggregates ───────────────────────────────────────────────

// GenerateRequest is the pure-domain input for a script generation.
// The builder (ingress registry) is demoted to producing exactly this
// type from raw payload — no network, no database, no Google Drive.
//
// Verdetto invariant: the builder must limit itself to:
//
//	func BuildGenerateRequest(raw map[string]any) (GenerateRequest, error)
//
// Zero I/O in the builder.
type GenerateRequest struct {
	Audio capabilityaudio.AudioMode `json:"audio_mode,omitempty"`
	// RenderFrameRate is optional for legacy requests; when present it is
	// preserved as an exact rational and compiled through FrameResolver.
	RenderFrameRate *capabilityaudio.FrameRate `json:"render_frame_rate,omitempty"`
	// IdempotencyKey is the caller-supplied idempotency key.
	IdempotencyKey string `json:"idempotency_key"`

	// ForceRefresh requests a new run when the idempotency key already
	// has an associated run. It mirrors the submission-layer intent so
	// the run ledger does not create duplicates on replay.
	ForceRefresh bool `json:"force_refresh,omitempty"`

	// Source describes the generation input source.
	Source Source `json:"source"`
	// ScriptParams carries the canonical sizing and ordered segment contract
	// from the envelope into the durable runtime. Dropping it here makes the
	// scene planner fall back to an unbounded prose envelope.
	ScriptParams scriptpkg.ScriptSpec `json:"script_params,omitempty"`

	// SourceLanguage is the primary language of the input (e.g. "en").
	// Scenes in this language are NOT translated.
	SourceLanguage Language `json:"source_language"`

	// Languages lists the target languages for translation and docs.
	Languages []Language `json:"languages"`

	// RenderVideo triggers the render enqueue step at the end.
	RenderVideo bool `json:"render_video"`

	// Docs is the explicit document publishing config.
	// Verdetto: document creation MUST be explicit (docs.enabled),
	// NOT implicit based on whether drive_output_folder is present.
	// One document per language is created, not one bilingual doc.
	Docs DocumentsConfig `json:"docs"`

	// DEPRECATED: use Docs.Enabled instead. Kept for backward compat.
	// Remove after all callers migrate to the Docs config struct.
	DocsEnabled bool `json:"docs_enabled,omitempty"`

	// DEPRECATED: use Docs.FolderID instead. Kept for backward compat.
	// Remove after all callers migrate to the Docs config struct.
	DriveFolderID string `json:"drive_folder_id,omitempty"`

	// Title is the output title (mirrors the caller's title).
	Title string `json:"title,omitempty"`

	// OutputName is the caller-specified output filename.
	OutputName string `json:"output_name,omitempty"`
}

// Scene represents a single scene within the generated output.
// Each scene has a deterministic ID, an ordered index, an optional
// clip reference, generated text per language, and an optional
// voiceover per language.
type Scene struct {
	ID         string `json:"id"`
	Index      int    `json:"index"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	// DurationUS is the sealed internal timing value. DurationMS remains only
	// as a legacy wire field at the boundary.
	DurationUS   int64                         `json:"duration_us,omitempty"`
	Clip         *ClipReference                `json:"clip,omitempty"`
	Clips        []*ClipReference              `json:"clips,omitempty"`
	Text         map[Language]string           `json:"text"`
	Voiceover    map[Language]AudioReference   `json:"voiceover,omitempty"`
	Audio        capabilityaudio.AudioIntent   `json:"audio"`
	AudioIntents []capabilityaudio.AudioIntent `json:"audio_intents,omitempty"`
}

// GenerateResult is the complete output of a script generation run.
// It carries every artifact produced by the workflow.
type GenerateResult struct {
	// SourceTrace is the durable retrieval trace. It is kept alongside the
	// capability result so the broker/API cannot lose the accepted Qdrant
	// clip IDs between the durable runner and the legacy job envelope.
	SourceTrace scriptpkg.SourceTrace `json:"source,omitempty"`
	// Scenes is the ordered list of generated scenes.
	Scenes []Scene `json:"scenes"`
	// ResolvedScenes is the sealed technical projection consumed by canonical
	// timeline/audio/render compilation. Scenes remain editorial input.
	ResolvedScenes []ResolvedScene `json:"resolved_scenes,omitempty"`

	// CanonicalTimeline and AudioPlan are persisted so the renderer and
	// audio compiler consume the same timing decision rather than deriving
	// independent offsets on either side of the enqueue boundary.
	CanonicalTimeline *capabilityaudio.CanonicalTimeline `json:"canonical_timeline,omitempty"`
	AudioPlan         *capabilityaudio.CompiledAudioPlan `json:"audio_plan,omitempty"`
	RenderPlan        *capabilityrender.RenderPlan       `json:"render_plan,omitempty"`

	// Documents maps each language to the published Google Doc.
	Documents               map[Language]DocumentReference `json:"documents,omitempty"`
	DocumentRenderers       map[Language]string            `json:"document_renderers,omitempty"`
	DocumentSpecSceneSHA256 map[Language]string            `json:"document_specscene_sha256,omitempty"`
	DocumentSceneCounts     map[Language]int               `json:"document_scene_counts,omitempty"`

	// RenderJob is non-nil when the workflow enqueued a render.
	RenderJob *RenderReference `json:"render_job,omitempty"`

	// Title is the output title (mirrors GenerateRequest.Title).
	Title string `json:"title,omitempty"`

	// OutputName is the caller-specified output name.
	OutputName string `json:"output_name,omitempty"`

	// WordCount is the total generated word count.
	WordCount      int    `json:"word_count"`
	VoiceoverGroup string `json:"voiceover_group,omitempty"`

	AudioMode     capabilityaudio.AudioMode           `json:"audio_mode,omitempty"`
	AudioStrategy capabilityaudio.AudioRenderStrategy `json:"audio_strategy,omitempty"`
	FinalAudio    *FinalAudioReference                `json:"final_audio,omitempty"`
	AudioMetrics  *AudioPipelineMetrics               `json:"audio_metrics,omitempty"`
}

// GenerationRun is the canonical aggregate that tracks a single
// execution of the script generation workflow. It is created BEFORE
// any external I/O (verdetto invariant: the POST handler creates
// the pipeline_run first).
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
	StageNormalizing           Stage = "NORMALIZING"
	StageGeneratingSceneText   Stage = "GENERATING_SCENE_TEXT"
	StageTranslatingScenes     Stage = "TRANSLATING_SCENES"
	StageGeneratingVoiceovers  Stage = "GENERATING_VOICEOVERS"
	StagePublishingDocuments   Stage = "PUBLISHING_DOCUMENTS"
	StageBuildingRenderPayload Stage = "BUILDING_RENDER_PAYLOAD"
	StageEnqueuingRender       Stage = "ENQUEUING_RENDER"
	StageWorkerQueued          Stage = "WORKER_QUEUED"
	StageCompleted             Stage = "COMPLETED"
	StageFailed                Stage = "FAILED"
)

// ── Document config helper ──────────────────────────────────────────

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

// IsRunCompletable checks whether a run has satisfied all preconditions
// for transitioning to COMPLETED. Verdetto contract:
//
//	scene normalizzate > 0
//	ogni scena ha testo EN
//	ogni scena ha testo ES
//	ogni voiceover richiesto è READY
//	documento EN ha id e link
//	documento ES ha id e link
//	render_job_id presente, quando render_video=true
//	nessuna fase richiesta è PENDING
func IsRunCompletable(result *GenerateResult, wantedLanguages []Language, renderVideo bool) bool {
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

	// Every wanted language must have a published document with ID and link.
	for _, lang := range wantedLanguages {
		doc, ok := result.Documents[lang]
		if !ok || doc.ID == "" || doc.Link == "" {
			return false
		}
	}

	// When render_video is true, a render job must exist.
	if renderVideo && (result.RenderJob == nil || result.RenderJob.JobID == "") {
		return false
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
	StageGeneratingSceneText,
	StageTranslatingScenes,
	StageGeneratingVoiceovers,
	StageBuildingRenderPayload,
	StageEnqueuingRender,
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
		// Already running — resume from current stage.
		if run.CurrentStage != "" && StageIndex(run.CurrentStage) >= 0 {
			return run.CurrentStage
		}
		return StageNormalizing
	default:
		return StageNormalizing
	}
}
