package ports

import "context"

// SegmentUnderstandingModel supplies semantic interpretation for one segment.
// Named entities are intentionally absent: deterministic NLP remains the sole
// owner of entity extraction and callers merge those entities separately.
type SegmentUnderstandingModel interface {
	Understand(context.Context, SegmentUnderstandingRequest) (SegmentUnderstandingResult, error)
}

// SegmentUnderstandingRequest is the small-model input contract.
type SegmentUnderstandingRequest struct {
	SegmentID     string `json:"segment_id,omitempty"`
	Text          string `json:"text"`
	Language      string `json:"language,omitempty"`
	Model         string `json:"model,omitempty"`
	PromptVersion string `json:"prompt_version,omitempty"`
}

// SegmentUnderstandingResult contains semantic fields only. Do not add
// entities here; entities are supplied by EntityExtractor/NLP.
type SegmentUnderstandingResult struct {
	Topic            string   `json:"topic,omitempty"`
	Subtopics        []string `json:"subtopics,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	VisualTerms      []string `json:"visual_terms,omitempty"`
	ImportantPhrases []string `json:"important_phrases,omitempty"`
	Actions          []string `json:"actions,omitempty"`
	VisualConcepts   []string `json:"visual_concepts,omitempty"`
	Retrieval        *struct {
		YouTube []string `json:"youtube,omitempty"`
		Artlist []string `json:"artlist,omitempty"`
		Images  []string `json:"images,omitempty"`
	} `json:"retrieval,omitempty"`
}
