// Package translation defines the canonical domain types for content
// translation (Fase 0 della Spina Dorsale, July 2026).
//
// Translation is a cross-cutting concern used by scripts, voiceover,
// books, and metadata pipelines. This package is the single source of
// truth for the translation contract — every consumer defines the
// need, but no consumer implements provider selection, caching, retry,
// or fallback logic.
//
// Ownership boundary:
//   - Consumer domains (scripts, voiceover, books, metadata) build a
//     TranslationCommand and call the unified TranslationService.
//   - The TranslationService (internal/application/translation/) owns
//     provider selection, model routing, caching, retry, and fallback.
//   - No consumer domain imports an LLM client or translation provider
//     directly — they only depend on this package's types.
//
// Canonical reference: Piano d'Azione § Fase 9.
package translation

// ── ContentKind ──────────────────────────────────────────────────────

// ContentKind classifies the type of content being translated.
// The translation service may use this to select the most appropriate
// model or to apply content-specific preservation rules (e.g. scripts
// preserve scene markers, metadata preserves field boundaries).
type ContentKind string

const (
	// ContentKindScript is a video script with scene markers.
	ContentKindScript ContentKind = "script"

	// ContentKindVoiceover is text destined for TTS audio.
	ContentKindVoiceover ContentKind = "voiceover"

	// ContentKindBook is long-form book or manuscript content.
	ContentKindBook ContentKind = "book"

	// ContentKindMetadata is short metadata fields (tags, descriptions).
	ContentKindMetadata ContentKind = "metadata"

	// ContentKindGeneral is uncategorised content.
	ContentKindGeneral ContentKind = "general"
)

// ── PreservationPolicy ───────────────────────────────────────────────

// PreservationPolicy controls what structural elements the translator
// must preserve during translation.
type PreservationPolicy struct {
	// PreserveFormatting keeps markdown, HTML tags, and line breaks
	// intact across the translation boundary.
	PreserveFormatting bool `json:"preserve_formatting"`

	// PreserveEntities keeps proper nouns, brand names, and technical
	// terms untranslated.
	PreserveEntities bool `json:"preserve_entities"`

	// PreserveSceneMarkers keeps scene boundary markers (e.g.
	// "<!-- SCENE 1 -->") in the original positions.
	PreserveSceneMarkers bool `json:"preserve_scene_markers"`
}

// ── ModelPolicy ──────────────────────────────────────────────────────

// ModelPolicy controls which translation model to use and how.
type ModelPolicy string

const (
	// ModelPolicyFast uses a lightweight, low-latency model suitable
	// for interactive or high-throughput scenarios.
	ModelPolicyFast ModelPolicy = "fast"

	// ModelPolicyQuality uses the best available model for accuracy
	// at the cost of higher latency.
	ModelPolicyQuality ModelPolicy = "quality"

	// ModelPolicyAuto lets the translation service choose based on
	// content length and ContentKind heuristics.
	ModelPolicyAuto ModelPolicy = "auto"
)

// ── TranslationCommand ───────────────────────────────────────────────

// TranslationCommand is the canonical request for translating a block
// of text. Every consumer domain (scripts, voiceover, books, metadata)
// builds this struct and calls the unified TranslationService.
type TranslationCommand struct {
	// SourceText is the text to translate. Required.
	SourceText string `json:"source_text"`

	// SourceLanguage is the BCP-47 language tag of the source text
	// (e.g. "en", "it", "pt-BR"). Empty means auto-detect.
	SourceLanguage string `json:"source_language,omitempty"`

	// TargetLanguage is the BCP-47 language tag to translate into.
	// Required.
	TargetLanguage string `json:"target_language"`

	// ContentKind classifies the content type for model selection.
	ContentKind ContentKind `json:"content_kind"`

	// Preserve controls structural preservation rules.
	Preserve PreservationPolicy `json:"preserve,omitempty"`

	// ModelPolicy selects the model tier (fast, quality, auto).
	ModelPolicy ModelPolicy `json:"model_policy,omitempty"`
}

// ── TranslationResult ────────────────────────────────────────────────

// TranslationResult is the canonical output of a translation operation.
// It carries the translated text plus provenance metadata for
// observability and cache auditing.
type TranslationResult struct {
	// Text is the translated output text.
	Text string `json:"text"`

	// SourceLanguage is the detected or provided source language tag.
	SourceLanguage string `json:"source_language"`

	// TargetLanguage is the target language tag (echoed from command).
	TargetLanguage string `json:"target_language"`

	// Provider is the translation service identifier used
	// (e.g. "ollama", "deepl", "google-translate").
	Provider string `json:"provider"`

	// Model is the specific model name used (e.g. "llama3:8b").
	Model string `json:"model"`

	// CacheStatus indicates whether the result was served from cache.
	// Typical values: "hit", "miss", "bypass".
	CacheStatus string `json:"cache_status"`
}
