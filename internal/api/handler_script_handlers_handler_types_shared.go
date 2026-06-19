package api

import (
	"reflect"
	"strings"
)

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

// ToMap converts any struct to map[string]any using reflection.
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


