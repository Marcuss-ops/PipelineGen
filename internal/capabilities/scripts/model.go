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

// ── Value types ─────────────────────────────────────────────────────

// Language is an ISO 639-1 two-letter code (e.g. "en", "es").
type Language string

// Source describes where the generation input comes from.
type Source struct {
	Type       SourceType `json:"type"`
	Topic      string     `json:"topic,omitempty"`
	SourceText string     `json:"source_text,omitempty"`
	ClipIDs    []string   `json:"clip_ids,omitempty"`
	Query      string     `json:"query,omitempty"`
	MaxClips   int        `json:"max_clips,omitempty"`
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
	ID       string  `json:"id"`
	SourceID string  `json:"source_id,omitempty"`
	Title    string  `json:"title,omitempty"`
	Duration float64 `json:"duration,omitempty"` // seconds
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

// RenderReference identifies an enqueued render job.
type RenderReference struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
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
	// IdempotencyKey is the caller-supplied idempotency key.
	IdempotencyKey string `json:"idempotency_key"`

	// ForceRefresh requests a new run when the idempotency key already
	// has an associated run. It mirrors the submission-layer intent so
	// the run ledger does not create duplicates on replay.
	ForceRefresh bool `json:"force_refresh,omitempty"`

	// Source describes the generation input source.
	Source Source `json:"source"`

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
	ID        string                      `json:"id"`
	Index     int                         `json:"index"`
	Clip      *ClipReference              `json:"clip,omitempty"`
	Text      map[Language]string         `json:"text"`
	Voiceover map[Language]AudioReference `json:"voiceover,omitempty"`
}

// GenerateResult is the complete output of a script generation run.
// It carries every artifact produced by the workflow.
type GenerateResult struct {
	// Scenes is the ordered list of generated scenes.
	Scenes []Scene `json:"scenes"`

	// Documents maps each language to the published Google Doc.
	Documents map[Language]DocumentReference `json:"documents,omitempty"`

	// RenderJob is non-nil when the workflow enqueued a render.
	RenderJob *RenderReference `json:"render_job,omitempty"`

	// Title is the output title (mirrors GenerateRequest.Title).
	Title string `json:"title,omitempty"`

	// OutputName is the caller-specified output name.
	OutputName string `json:"output_name,omitempty"`

	// WordCount is the total generated word count.
	WordCount int `json:"word_count"`
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
	StagePublishingDocuments,
	StageBuildingRenderPayload,
	StageEnqueuingRender,
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
