// Package scriptjobs defines the typed contract for script generation jobs.
//
// GenerationSpec is the single source of truth for all script generation
// parameters. It is used across the full pipeline:
//
//	HTTP request → GenerationSpec → GeneratePayload → worker decode
//
// This eliminates the triplication between GenerateFromClipsRequest,
// FromClipsCommand, jobPayloadUnified, and map[string]any.
package script

// (Removed June 2026, Wave 5 PR3) JobTypeGenerateFromClips was a duplicate
// of job.TypeClipScriptGenerate in internal/domain/job/job.go. The job
// broker is the canonical owner of job-type strings per
// architecture/ownership.yaml; route all dispatch through
//   job.TypeClipScriptGenerate
// (which holds the value "script.generate_from_clips").

// GenerationSpec is the canonical payload for script generation.
// It contains every field the worker needs to execute the generation
// pipeline, from text input through clip selection to output options.
//
// Fields use json tags with omitempty for backward compatibility
// with older payloads that may omit optional fields.
type GenerationSpec struct {
	// ── Text generation ──────────────────────────────────────────────
	Topic      string `json:"topic,omitempty"`
	SourceText string `json:"source_text,omitempty"`
	Guidelines string `json:"guidelines,omitempty"`

	// ── Clip-aware ───────────────────────────────────────────────────
	ClipIDs  []string `json:"clip_ids,omitempty"`
	NumClips int      `json:"num_clips,omitempty"`

	// ── Identity ─────────────────────────────────────────────────────
	Title         string `json:"title,omitempty"`
	OutputName    string `json:"output_name,omitempty"`
	Language      string `json:"language,omitempty"`
	Tone          string `json:"tone,omitempty"`
	Style         string `json:"style,omitempty"`
	Model         string `json:"model,omitempty"`
	DriveFolderID string `json:"drive_folder_id,omitempty"`

	// ── Sizing ───────────────────────────────────────────────────────
	TargetWords       int `json:"target_words,omitempty"`
	Duration          int `json:"duration,omitempty"`
	MinWords          int `json:"min_words,omitempty"`
	SentencesPerImage int `json:"sentences_per_image,omitempty"`
	ImagesPerScene    int `json:"images_per_scene,omitempty"`

	// ── Feature flags ────────────────────────────────────────────────
	ExtractEntities     bool   `json:"extract_entities,omitempty"`
	ArtlistSearch       bool   `json:"artlist_search,omitempty"`
	StockSearch         bool   `json:"stock_search,omitempty"`
	GenerateMetadata    bool   `json:"generate_metadata,omitempty"`
	GenerateVoiceover   bool   `json:"generate_voiceover,omitempty"`
	VoiceoverGroup      string `json:"voiceover_group,omitempty"`
	VoiceoverFolderID   string `json:"voiceover_folder_id,omitempty"`
	GenerateSceneImages bool   `json:"generate_scene_images,omitempty"`

	// ── Multilingual ─────────────────────────────────────────────────
	Languages []string `json:"languages,omitempty"`

	// ── Clip pipeline options ────────────────────────────────────────
	TranscriptPolicy string `json:"transcript_policy,omitempty"`
	OrderingStrategy string `json:"ordering_strategy,omitempty"`
	SaveToDB         bool   `json:"save_to_db,omitempty"`
	GenerateTimeline bool   `json:"generate_timeline,omitempty"`
	ForceRefresh     bool   `json:"force_refresh,omitempty"`

	// ── Quality thresholds ───────────────────────────────────────────
	MinQualityScore    *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int     `json:"min_transcript_words,omitempty"`

	// ── Output formatting ────────────────────────────────────────────
	MaxChars  int    `json:"max_chars,omitempty"`
	OutputFmt string `json:"output_fmt,omitempty"` // "prose" (default) or "json"

	// ── Prompt versioning ────────────────────────────────────────────
	PromptVersion       string `json:"prompt_version,omitempty"`
	EditorPromptVersion string `json:"editor_prompt_version,omitempty"`
	QAPromptVersion     string `json:"qa_prompt_version,omitempty"`
}

// HasClips returns true when the spec requests clip-aware generation
// (explicit clip IDs or automatic search via NumClips).
func (s *GenerationSpec) HasClips() bool {
	return len(s.ClipIDs) > 0 || s.NumClips > 0
}

// HasText returns true when a text topic or source text is provided
// for text-only generation.
func (s *GenerationSpec) HasText() bool {
	return s.Topic != "" || s.SourceText != ""
}
