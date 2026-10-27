package script

func cachedProfile() SegmentSemanticProfile {
	return SegmentSemanticProfile{
		SegmentID:                 "segment-001",
		TextHash:                  "text-hash-1",
		UnderstandingModelVersion: "gemma3:1b",
		PromptVersion:             "segment_semantics_v1",
		Topic:                     "origine dei trattori",
		Subtopics:                 []string{"agricoltura"},
		Keywords:                  []WeightedKeyword{{Value: "tractor", Confidence: 1}},
		VisualTerms:               []WeightedKeyword{{Value: "vintage tractor", Confidence: 1}},
		Entities:                  []ExtractedEntity{{Value: "Iowa", Type: "PLACE", Confidence: 0.9}},
		Retrieval:                 &RetrievalIntent{YouTube: []string{"tractor history"}},
	}
}
