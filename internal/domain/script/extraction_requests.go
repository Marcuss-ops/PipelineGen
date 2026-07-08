// Package script — extraction_requests.go defines the typed request
// shapes for the post-generation entity extraction + metadata
// generation ports. Both ports consume canonical typed structs
// (PR 3, June 2026) so callers cannot accidentally regress to the
// opaque-string `EntitiesJSON` field shape that pre-PR-3 code used.
//
// Companion to domain/script/generation_result.go::EntityResult and
// ::VideoMetadata. The canonical V1 producer chain is:
//
//	generated script text → EntityExtractionRequest → EntityExtractor port → *EntityResult
//	generated script text → MetadataGenerationRequest → MetadataGenerator port → []VideoMetadata
//
// No durable field uses interface{}, any, or map[string]any.
package script

// Entity is one item extracted by the entities processor.
//
// PR 3 (June 2026): typed shape replaces the pre-PR-3 free-form
// string-array. Value is the canonical entity name; Score (when
// present) is the confidence returned by the entity extractor.
type Entity struct {
	Value string  `json:"value"`
	Score float32 `json:"score,omitempty"`
}

// EntityResult is the typed entity extraction output. Carries
// grouped slots (Persons, Places, Concepts) plus a Raw field for
// backward read-compat with pre-PR-3 untyped JSON dump rows.
//
// PR 3 (June 2026): introduced to replace the pre-PR-3 EntitiesJSON
// string. The Persons/Places/Concepts slices are empty by default —
// the entity extractor is responsible for parsing the postgen LLM
// output into these slots. Empty slices still yield a valid
// EntityResult (callers see a consistent shape across all
// generation flows).
//
// Producers MUST populate this struct directly via the
// EntityExtractor port. Consumers MUST read typed fields
// (Persons / Places / Concepts) rather than parsing Raw. Raw is
// retained ONLY for backward read-compat with rows written before
// PR 3.
type EntityResult struct {
	Persons  []Entity `json:"persons,omitempty"`
	Places   []Entity `json:"places,omitempty"`
	Concepts []Entity `json:"concepts,omitempty"`
	// ArtlistPhrases are visual/search phrases extracted per
	// segment (PR-ENTITY-EXTRACTOR-WIRING, July 2026). Populated
	// by the EntityExtractor adapter from the Ollama backend.
	// Downstream consumers (InsightBuilder, SearchArtlistClips)
	// read this typed field to search for matching Artlist clips.
	ArtlistPhrases []string `json:"artlist_phrases,omitempty"`
	// ImportantPhrases are key phrases extracted per segment
	// (PR-ENTITY-EXTRACTOR-WIRING, July 2026). Maps from the
	// Ollama backend's frasi_importanti field — narrative
	// fragments that capture the essence of each segment.
	ImportantPhrases []string `json:"important_phrases,omitempty"`
	// ImportantWords are key concepts/words extracted per
	// segment (PR-ENTITY-EXTRACTOR-WIRING, July 2026). Maps
	// from the Ollama backend's parole_importanti — mirrors
	// Concepts but uses a clearer field name aligned with the
	// Italian schema (parole_importanti = important words).
	ImportantWords []string `json:"important_words,omitempty"`
	// Raw is the original postgen LLM JSON string, kept for
	// backward read-compat with rows written before PR 3.
	Raw string `json:"raw,omitempty"`
}

// EntityExtractionRequest is the canonical V1 typed request shape
// for entity extraction. The processor consumes ProcessInput.Text
// as the script body and threads the ResolvedGenerationPlan identity
// fields (Title / Language / Model) plus the typed SpecScene.
type EntityExtractionRequest struct {
	// Text is the canonical script body — populated from
	// ProcessInput.Text (model output).
	Text string `json:"text"`
	// Title is the resolved document/video title.
	Title string `json:"title,omitempty"`
	// Language is the canonical target language (ISO 639-1).
	Language string `json:"language,omitempty"`
	// Model is the engine model identifier.
	Model string `json:"model,omitempty"`
	// SpecScene carries the structured scene breakdown so the
	// extractor can tie entity mentions to specific scenes
	// (e.g. fallback heuristics when the prose is ambiguous).
	SpecScene SpecSceneOutput `json:"specscene,omitempty"`
}

// MetadataGenerationRequest is the canonical V1 typed request
// shape for metadata generation. The processor consumes
// ProcessInput.Text as the script body and threads the
// ResolvedGenerationPlan identity fields (Title / Language / Model).
type MetadataGenerationRequest struct {
	// Text is the canonical script body — populated from
	// ProcessInput.Text (model output).
	Text string `json:"text"`
	// Title is the resolved document/video title.
	Title string `json:"title,omitempty"`
	// Language is the canonical target language for this
	// metadata entry (ISO 639-1). The backend returns one
	// VideoMetadata per language requested.
	Language string `json:"language,omitempty"`
	// Model is the engine model identifier.
	Model string `json:"model,omitempty"`
	// SpecScene is reserved for scene-anchored metadata
	// extraction in a follow-up PR; ignored today.
	SpecScene SpecSceneOutput `json:"specscene,omitempty"`
}
