// Package scriptjobs defines the typed contract for script generation jobs.
//
// GenerationSpec is the canonical parameters struct for script generation.
// It is used by the post-processor pipeline (PostGenUseCase, EntityProcessor,
// MetadataProcessor) to carry generation flags.
//
// PR 12 (June 2026): GeneratePayload / DecodeGeneratePayload removed —
// the worker now decodes GenerationEnvelopeV2 via DecodeEnvelopeV2.
// GenerationSpec remains as a parameter carrier for the post-gen pipeline.
package script

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
	ExtractEntities   bool   `json:"extract_entities,omitempty"`
	ArtlistSearch     bool   `json:"artlist_search,omitempty"`
	StockSearch       bool   `json:"stock_search,omitempty"`
	GenerateMetadata  bool   `json:"generate_metadata,omitempty"`
	VoiceoverGroup    string `json:"voiceover_group,omitempty"`
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`

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
	OutputFmt string `json:"output_fmt,omitempty"` // PR 9: "json" (canonical default); "prose" is REJECTED by the validator (PR 6)

	// ── Prompt versioning ────────────────────────────────────────────
	PromptVersion       string `json:"prompt_version,omitempty"`
	EditorPromptVersion string `json:"editor_prompt_version,omitempty"`
	QAPromptVersion     string `json:"qa_prompt_version,omitempty"`
}
