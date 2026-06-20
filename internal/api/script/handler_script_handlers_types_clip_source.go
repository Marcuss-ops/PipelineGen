package script

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
)

// GenerateFromClipsRequest is the unified input for POST /api/script/generate-from-clips.
// Supports both text-only (num_clips=0, no clip_ids) and clip-aware (clip_ids or num_clips>0) generation.
// Always async — the endpoint enqueues a background job.
type GenerateFromClipsRequest struct {
	// ── Text generation fields ─────────────────────────────────────────────
	// Used when num_clips == 0 and no clip_ids provided (text-only mode).
	Topic      string `json:"topic,omitempty"`
	SourceText string `json:"source_text,omitempty"`
	Guidelines string `json:"guidelines,omitempty"`

	// ── Clip-aware fields ──────────────────────────────────────────────────
	// Provide clip_ids explicitly, or set num_clips > 0 for automatic search.
	ClipIDs  []string `json:"clip_ids,omitempty"`
	NumClips int      `json:"num_clips,omitempty"`

	// ── Identity ───────────────────────────────────────────────────────────
	Title         string `json:"title,omitempty"`
	OutputName    string `json:"output_name,omitempty"`
	Language      string `json:"language,omitempty"`
	Tone          string `json:"tone,omitempty"`
	Style         string `json:"style,omitempty"`
	Model         string `json:"model,omitempty"`
	DriveFolderID string `json:"drive_folder_id,omitempty"`

	// ── Sizing ─────────────────────────────────────────────────────────────
	TargetWords       int `json:"target_words,omitempty"`
	Duration          int `json:"duration,omitempty"`
	MinWords          int `json:"min_words,omitempty"`
	SentencesPerImage int `json:"sentences_per_image,omitempty"`
	ImagesPerScene    int `json:"images_per_scene,omitempty"`

	// ── Feature flags (all selectable individually) ────────────────────────
	ExtractEntities     bool   `json:"extract_entities,omitempty"`
	ArtlistSearch       bool   `json:"artlist_search,omitempty"`
	StockSearch         bool   `json:"stock_search,omitempty"`
	GenerateMetadata    bool   `json:"generate_metadata,omitempty"`
	GenerateVoiceover   bool   `json:"generate_voiceover,omitempty"`
	VoiceoverGroup      string `json:"voiceover_group,omitempty"`
	VoiceoverFolderID   string `json:"voiceover_folder_id,omitempty"`
	GenerateSceneImages bool   `json:"generate_scene_images,omitempty"`
	// Google Doc è sempre creato (non serve flag)

	// ── Multilingual ───────────────────────────────────────────────────────
	Languages []string `json:"languages,omitempty"`

	// ── Clip pipeline options ──────────────────────────────────────────────
	TranscriptPolicy string `json:"transcript_policy,omitempty"`
	OrderingStrategy string `json:"ordering_strategy,omitempty"`

	SaveToDB         bool `json:"save_to_db,omitempty"`
	GenerateTimeline bool `json:"generate_timeline,omitempty"`
	ForceRefresh     bool `json:"force_refresh,omitempty"`

	// ── Quality thresholds ─────────────────────────────────────────────────
	MinQualityScore    *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int     `json:"min_transcript_words,omitempty"`

	// ── Prompt versioning ──────────────────────────────────────────────────
	PromptVersion       string `json:"prompt_version,omitempty"`
	EditorPromptVersion string `json:"editor_prompt_version,omitempty"`
	QAPromptVersion     string `json:"qa_prompt_version,omitempty"`
}

// GenerateFromClipsResponse is the response for the async endpoint.
type GenerateFromClipsResponse struct {
	OK        bool   `json:"ok"`
	JobID     string `json:"job_id"`
	Status    string `json:"status"`
	ClipCount int    `json:"clip_count"`
}

// GenerateWithImagesRequest is the input for POST /api/script/generate-with-images.
// Dedicated endpoint for script + scene-by-scene AI image generation.
// Always generates scene images; entity extraction and metadata are disabled.
type GenerateWithImagesRequest struct {
	// ── Text generation fields ─────────────────────────────────────────────
	Topic      string `json:"topic,omitempty"`
	SourceText string `json:"source_text,omitempty"`
	Guidelines string `json:"guidelines,omitempty"`

	// ── Clip-aware fields ──────────────────────────────────────────────────
	ClipIDs  []string `json:"clip_ids,omitempty"`
	NumClips int      `json:"num_clips,omitempty"`

	// ── Identity ───────────────────────────────────────────────────────────
	Title         string `json:"title,omitempty"`
	OutputName    string `json:"output_name,omitempty"`
	Language      string `json:"language,omitempty"`
	Tone          string `json:"tone,omitempty"`
	Style         string `json:"style,omitempty"`
	Model         string `json:"model,omitempty"`
	DriveFolderID string `json:"drive_folder_id,omitempty"`

	// ── Sizing ─────────────────────────────────────────────────────────────
	TargetWords       int `json:"target_words,omitempty"`
	Duration          int `json:"duration,omitempty"`
	MinWords          int `json:"min_words,omitempty"`
	SentencesPerImage int `json:"sentences_per_image,omitempty"`
	ImagesPerScene    int `json:"images_per_scene,omitempty"`

	// ── Feature flags ──────────────────────────────────────────────────────
	ArtlistSearch     bool   `json:"artlist_search,omitempty"`
	StockSearch       bool   `json:"stock_search,omitempty"`
	GenerateVoiceover bool   `json:"generate_voiceover,omitempty"`
	VoiceoverGroup    string `json:"voiceover_group,omitempty"`
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`

	// ── Multilingual ───────────────────────────────────────────────────────
	Languages []string `json:"languages,omitempty"`

	// ── Clip pipeline options ──────────────────────────────────────────────
	TranscriptPolicy string `json:"transcript_policy,omitempty"`
	OrderingStrategy string `json:"ordering_strategy,omitempty"`

	SaveToDB         bool `json:"save_to_db,omitempty"`
	GenerateTimeline bool `json:"generate_timeline,omitempty"`
	ForceRefresh     bool `json:"force_refresh,omitempty"`

	// ── Quality thresholds ─────────────────────────────────────────────────
	MinQualityScore    *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int     `json:"min_transcript_words,omitempty"`

	// ── Prompt versioning ──────────────────────────────────────────────────
	PromptVersion       string `json:"prompt_version,omitempty"`
	EditorPromptVersion string `json:"editor_prompt_version,omitempty"`
	QAPromptVersion     string `json:"qa_prompt_version,omitempty"`
}

// ScriptSceneImage is an alias for scripts.SceneImage (canonical type in application layer).
//
// Kind + NarrationRole were added (June 2026, generate-from-clips
// endpoint-compat request) so the user-facing `scenes[]` array always carries
// machine-detectable intro/outro labels regardless of whether the writer LLM
// emitted [Narration: ...] markers. The helper `scenes.markScenesIntroOutro`
// (flow_scene_intro_outro.go) labels the first scene as intro and the last as
// outro — see that file for the exact policy.
//
// Article targets in the JSON contract:
//   - "kind"          : "narration" | "content"  (default omitted)
//   - "narration_role": "intro" | "outro" | "transition" (only when kind=="narration")
//
// omitempty on both fields so existing clients reading `text/image/images`
// without these new fields keep working unchanged.
type ScriptSceneImage = scripts.SceneImage

// SceneVoiceover is an alias for scripts.SceneVoiceover (canonical type in application layer).
type SceneVoiceover = scripts.SceneVoiceover

// ClipScriptJobResult is the result stored in the job system after completion.
type ClipScriptJobResult struct {
	OK                bool                    `json:"ok"`
	ScriptID          int64                   `json:"script_id,omitempty"`
	Title             string                  `json:"title,omitempty"`
	Script            string                  `json:"script,omitempty"`
	WordCount         int                     `json:"word_count,omitempty"`
	Language          string                  `json:"language,omitempty"`
	SourceFingerprint string                  `json:"source_fingerprint,omitempty"`
	ClipCoverage      *scripts.ClipCoverage   `json:"clip_coverage,omitempty"`
	Sections          []scripts.ScriptSection `json:"sections,omitempty"`
	ExcludedClips     []scripts.ClipEvidence  `json:"excluded_clips,omitempty"`
	Warnings          []string                `json:"warnings,omitempty"`
	DocURL            string                  `json:"doc_url,omitempty"`
	DocID             string                  `json:"doc_id,omitempty"`

	// Entity/insight fields (populated when extract_entities is true)
	EntitiesJSON           string                        `json:"entities_json,omitempty"`
	ImportantWords         []string                      `json:"important_words,omitempty"`
	ImportantPhrases       []string                      `json:"important_phrases,omitempty"`
	SpecialNames           []string                      `json:"special_names,omitempty"`
	ArtlistPhrases         []string                      `json:"artlist_phrases,omitempty"`
	ArtlistClipSuggestions []ScriptArtlistClipSuggestion `json:"artlist_clip_suggestions,omitempty"`
	RecommendedDriveFolder *ScriptDriveFolderSuggestion  `json:"recommended_drive_folder,omitempty"`
	PhraseClipSuggestions  []ScriptPhraseClipSuggestion  `json:"phrase_clip_suggestions,omitempty"`
	IntroClips             []ScriptAssetSuggestion       `json:"intro_clips,omitempty"`
	EntityImages           []ScriptEntityImage           `json:"entity_images,omitempty"`
	Scenes                 []ScriptSceneImage            `json:"scenes,omitempty"`
	Voiceovers             []SceneVoiceover              `json:"voiceovers,omitempty"`

	// Metadata fields (populated when generate_metadata is true)
	Metadata []VideoMetadata `json:"metadata,omitempty"`
}
