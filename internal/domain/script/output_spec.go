// Package script — output_spec.go defines the canonical ScriptSpec
// (HOW to generate) and OutputSpec (WHAT to produce) contracts.
//
// No durable field uses interface{}, any, or map[string]any.
package script

// ScriptSpec controls the generation behaviour: sizing, style, and
// prompt versioning. Identity fields (Language, Tone, Model) live
// on GenerationItemV2; the normalizer merges them into the resolved
// plan. Every field is optional — the normalizer fills in defaults
// from presets and configuration.
type ScriptSpec struct {
	// ── Sizing ────────────────────────────────────────────────────────
	TargetWords       int `json:"target_words,omitempty"`
	Duration          int `json:"duration,omitempty"`
	MinWords          int `json:"min_words,omitempty"`
	SentencesPerImage int `json:"sentences_per_image,omitempty"`
	ImagesPerScene    int `json:"images_per_scene,omitempty"`

	// ── Style ─────────────────────────────────────────────────────────
	Style      string `json:"style,omitempty"`
	Guidelines string `json:"guidelines,omitempty"`

	// ── Clip pipeline ─────────────────────────────────────────────────
	TranscriptPolicy string `json:"transcript_policy,omitempty"`
	OrderingStrategy string `json:"ordering_strategy,omitempty"`

	// ── Prompt versioning ─────────────────────────────────────────────
	PromptVersion       string `json:"prompt_version,omitempty"`
	EditorPromptVersion string `json:"editor_prompt_version,omitempty"`
	QAPromptVersion     string `json:"qa_prompt_version,omitempty"`

	// ── Memory gate ───────────────────────────────────────────────────
	ForceRefresh bool `json:"force_refresh,omitempty"`
	UseMemory    bool `json:"use_memory,omitempty"`
}

// OutputSpec declares which post-generation artifacts to produce.
// Every processor is opt-in — it runs only when its flag is true
// and the corresponding service is wired at composition time.
type OutputSpec struct {
	// ── Postprocessors ────────────────────────────────────────────────
	ExtractEntities     bool `json:"extract_entities,omitempty"`
	GenerateMetadata    bool `json:"generate_metadata,omitempty"`
	GenerateVoiceover   bool `json:"generate_voiceover,omitempty"`
	GenerateSceneImages bool `json:"generate_scene_images,omitempty"`
	GenerateDocument    bool `json:"generate_document,omitempty"`

	// ── Persistence ───────────────────────────────────────────────────
	SaveToDB         bool `json:"save_to_db,omitempty"`
	GenerateTimeline bool `json:"generate_timeline,omitempty"`

	// ── Voiceover options ─────────────────────────────────────────────
	VoiceoverGroup    string `json:"voiceover_group,omitempty"`
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`

	// ── Document options ──────────────────────────────────────────────
	DriveFolderID string `json:"drive_folder_id,omitempty"`

	// ── Formatting ────────────────────────────────────────────────────
	MaxChars  int    `json:"max_chars,omitempty"`
	OutputFmt string `json:"output_fmt,omitempty"` // "prose" (default) or "json"

	// ── Translations ──────────────────────────────────────────────────
	Languages []string `json:"languages,omitempty"`
}

// HasAnyPostprocessor returns true when at least one postprocessor
// flag is enabled.
func (o *OutputSpec) HasAnyPostprocessor() bool {
	return o.ExtractEntities ||
		o.GenerateMetadata ||
		o.GenerateVoiceover ||
		o.GenerateSceneImages ||
		o.GenerateDocument
}
