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

	// Source describes the generation input source.
	Source Source `json:"source"`

	// SourceLanguage is the primary language of the input (e.g. "en").
	// Scenes in this language are NOT translated.
	SourceLanguage Language `json:"source_language"`

	// Languages lists the target languages for translation and docs.
	Languages []Language `json:"languages"`

	// RenderVideo triggers the render enqueue step at the end.
	RenderVideo bool `json:"render_video"`

	// DocsEnabled enables Google Doc publishing.
	DocsEnabled bool `json:"docs_enabled"`

	// DriveFolderID is the target Google Drive folder for documents.
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

	// Request is the original generation request.
	Request GenerateRequest `json:"request"`

	// Status is the high-level lifecycle status.
	Status RunStatus `json:"status"`

	// CurrentStage identifies the exact workflow phase.
	CurrentStage Stage `json:"current_stage"`

	// Result is populated when the run reaches a terminal state.
	Result *GenerateResult `json:"result,omitempty"`

	// ErrorCode and FailedStage capture the failure reason.
	ErrorCode   string `json:"error_code,omitempty"`
	FailedStage Stage  `json:"failed_stage,omitempty"`

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
