// Package script (api/script) — types.go carries all shared
// request, response and DTO types for the script-flow transport
// plus the BaseGenerateRequest ancestor and struct→map helpers
// used by job-payload marshalling.
//
// PR3 (June 2026): this file consolidates the three prior files:
//
//   types_clip_source.go    (request/response types + aliases)
//   handler_types_shared.go (BaseGenerateRequest + VideoMetadata + structToMap helpers)
//   language_markers.go     (looksTranslated delegating to pkg/textutil)
//
// Aliases preserved so existing imports of `script.GenerateFromClipsRequest`,
// `script.VideoMetadata`, `script.BaseGenerateRequest`, etc. continue to
// resolve without changes in callers.
package script

import (
	"reflect"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Generation request / response types ─────────────────────────────────────

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
	EntitiesJSON           string                         `json:"entities_json,omitempty"`
	ImportantWords         []string                       `json:"important_words,omitempty"`
	ImportantPhrases       []string                       `json:"important_phrases,omitempty"`
	SpecialNames           []string                       `json:"special_names,omitempty"`
	ArtlistPhrases         []string                       `json:"artlist_phrases,omitempty"`
	ArtlistClipSuggestions []scripts.ScriptArtlistClipSuggestion `json:"artlist_clip_suggestions,omitempty"`
	RecommendedDriveFolder *scripts.ScriptDriveFolderSuggestion  `json:"recommended_drive_folder,omitempty"`
	PhraseClipSuggestions  []scripts.ScriptPhraseClipSuggestion `json:"phrase_clip_suggestions,omitempty"`
	IntroClips             []scripts.ScriptAssetSuggestion      `json:"intro_clips,omitempty"`
	EntityImages           []scripts.ScriptEntityImage          `json:"entity_images,omitempty"`
	Scenes                 []scripts.SceneImage                 `json:"scenes,omitempty"`
	Voiceovers             []scripts.SceneVoiceover             `json:"voiceovers,omitempty"`

	// Metadata fields (populated when generate_metadata is true)
	Metadata []VideoMetadata `json:"metadata,omitempty"`
}

// ── Shared Base Request ──────────────────────────────────────────────────────
//
// BaseGenerateRequest holds the fields common to the active script generation
// endpoints (/generate-batch, /generate-from-clips). Each endpoint embeds this
// struct and adds its own fields, eliminating duplication.
//
// Note:
//   - /generate (legacy) was removed June 2026.
//
// All fields use omitempty so that serialised JSON omits zero-values;
// callers that require a field (e.g. Language) should set a default
// inside the handler before embedding.

// BaseGenerateRequest contains fields shared across all script generation
// endpoints: language, tone, model, channel identification, sizing hints,
// guidelines, prompt versions, persistence flags and timeout.
type BaseGenerateRequest struct {
	// Identity
	Language  string `json:"language,omitempty"`
	Tone      string `json:"tone,omitempty"`
	Model     string `json:"model,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`

	// Sizing
	Duration int `json:"duration,omitempty"`
	MinWords int `json:"min_words,omitempty"`

	// Style guidance
	Guidelines string `json:"guidelines,omitempty"`

	// Multilingual support
	Languages []string `json:"languages,omitempty"`

	// Prompt versioning — runtime selection of prompt template
	PromptVersion       string `json:"prompt_version,omitempty"`
	EditorPromptVersion string `json:"editor_prompt_version,omitempty"`
	QAPromptVersion     string `json:"qa_prompt_version,omitempty"`

	// Persistence
	SaveToDB      bool   `json:"save_to_db,omitempty"`
	DriveFolderID string `json:"drive_folder_id,omitempty"`

	// Memory gate
	UseMemory    *bool `json:"use_memory,omitempty"`
	ForceRefresh bool  `json:"force_refresh,omitempty"`

	// Timeout overrides the default request timeout (in seconds).
	// 0 means "use the endpoint default" (10 min single, 30 min batch).
	RequestTimeout int `json:"request_timeout_seconds,omitempty"`

	// Clip-aware generation fields (used when NumClips > 0)
	NumClips          int     `json:"num_clips,omitempty"`
	Source            string  `json:"source,omitempty"`
	MediaType         string  `json:"media_type,omitempty"`
	MinScore          float64 `json:"min_score,omitempty"`
	SelectableClips   int     `json:"selectable_clips,omitempty"`
	MaxCharsPerScene  int     `json:"max_chars_per_scene,omitempty"`
	Style             string  `json:"style,omitempty"`
	Type              string  `json:"type,omitempty"`
	StyleInstructions string  `json:"style_instructions,omitempty"`
}

// ── Shared Types ────────────────────────────────────────────────────────────

// VideoMetadata holds per-language YouTube metadata (title, description, tags).
type VideoMetadata struct {
	Language    string   `json:"language"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// ── Struct → map Utility ────────────────────────────────────────────────────

// structToMap converts any struct to map[string]any using reflection.
// It walks exported fields, respects `json` tags for key names and omitempty,
// and recursively converts nested structs, pointers, and slices.
// Maps and basic types are passed through directly.
func structToMap(v any) map[string]any {
	out := make(map[string]any)
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return out
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return out
	}
	typ := val.Type()
	for i := range val.NumField() {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		fv := val.Field(i)

		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		omitEmpty := opts == "omitempty"

		raw := fv.Interface()
		if omitEmpty && isZero(fv) {
			continue
		}
		out[name] = convertValue(raw)
	}
	return out
}

func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Ptr, reflect.Slice, reflect.Map:
		return v.IsNil()
	case reflect.Struct:
		// A struct is zero iff ALL its exported fields are zero.
		// Unexported fields are skipped — they don't participate in
		// JSON serialization. A struct with no exported fields is
		// considered zero.
		for i := range v.NumField() {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			if !isZero(v.Field(i)) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func convertValue(v any) any {
	if v == nil {
		return nil
	}
	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Ptr:
		if val.IsNil() {
			return nil
		}
		return convertValue(val.Elem().Interface())
	case reflect.Struct:
		return structToMap(v)
	case reflect.Slice:
		n := val.Len()
		out := make([]any, n)
		for i := range n {
			out[i] = convertValue(val.Index(i).Interface())
		}
		return out
	case reflect.Map:
		// Pass through — already map[string]any or similar
		return v
	default:
		return v
	}
}

// ── Language detection ───────────────────────────────────────────────────────

// looksTranslated delegates directly to the canonical implementation in pkg/textutil.
// Kept here as a thin re-export so existing in-package callers continue to work.
func looksTranslated(text, targetLang, sourceLang string) bool {
	return textutil.LooksTranslated(text, targetLang, sourceLang)
}
