// Package script defines typed requests for post-generation extraction.
package script

// Entity is one item extracted by the entities processor.
type Entity struct {
	Value string  `json:"value"`
	Type  string  `json:"type,omitempty"`
	Score float32 `json:"score,omitempty"`
}

// EntityResult is the canonical typed entity-extraction output.
//
// It is the extractor's typed output surface. It evolves into the canonical
// SegmentSemanticProfile via BuildSegmentSemanticProfile (the single
// canonical point — no parallel mapping is allowed); legacy consumers keep
// reading this surface.
type EntityResult struct {
	Persons          []Entity `json:"persons,omitempty"`
	Places           []Entity `json:"places,omitempty"`
	Concepts         []Entity `json:"concepts,omitempty"`
	ArtlistPhrases   []string `json:"artlist_phrases,omitempty"`
	ImportantPhrases []string `json:"important_phrases,omitempty"`
	ImportantWords   []string `json:"important_words,omitempty"`
	// Topic, Actions and VisualConcepts are semantic fields produced by the
	// small model. Named entities remain owned by deterministic NLP.
	Topic          string   `json:"topic,omitempty"`
	Subtopics      []string `json:"subtopics,omitempty"`
	Actions        []string `json:"actions,omitempty"`
	VisualConcepts []string `json:"visual_concepts,omitempty"`
	// Raw preserves the original backend analysis for backward reads and
	// diagnostics. Consumers should prefer the typed fields above.
	Raw string `json:"raw,omitempty"`
}

// EntityExtractionBatchResult preserves scene identity when several scene
// requests share one model invocation.
type EntityExtractionBatchResult struct {
	SegmentID    string        `json:"segment_id,omitempty"`
	SegmentIndex int           `json:"segment_index"`
	Result       *EntityResult `json:"result,omitempty"`
}

// EntityExtractionRequest is the canonical typed request for entity extraction.
type EntityExtractionRequest struct {
	// SegmentID is stable within the current VidRush plan and lets a batched
	// backend map the model response back to the originating scene.
	SegmentID string `json:"segment_id,omitempty"`
	// Text is the canonical text for exactly one VidRush segment.
	Text string `json:"text"`
	// Title is the resolved document/video title.
	Title string `json:"title,omitempty"`
	// Language is the canonical target language (ISO 639-1).
	Language string `json:"language,omitempty"`
	// Device selects the local inference backend: "auto", "cpu", or "gpu".
	// GPU is optional and must never be represented by a silent CPU no-op.
	Device string `json:"device,omitempty"`
	// Model is the engine model identifier.
	Model string `json:"model,omitempty"`
	// EntityCount is the requested maximum named-entity count for this
	// segment. Zero lets the adapter select its safe default.
	EntityCount int `json:"entity_count,omitempty"`
	// SpecScene carries the structured scene breakdown for adapters that
	// use scene context. VidRush callers should pass only the current scene.
	SpecScene                 SpecSceneOutput `json:"specscene,omitempty"`
	UnderstandingModelVersion string          `json:"understanding_model_version,omitempty"`
	PromptVersion             string          `json:"prompt_version,omitempty"`
}

// MetadataGenerationRequest is the canonical typed request for metadata generation.
type MetadataGenerationRequest struct {
	Text      string          `json:"text"`
	Title     string          `json:"title,omitempty"`
	Language  string          `json:"language,omitempty"`
	Model     string          `json:"model,omitempty"`
	SpecScene SpecSceneOutput `json:"specscene,omitempty"`
}
