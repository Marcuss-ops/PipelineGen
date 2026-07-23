package images

import (
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
)

// semanticPayloadToMap converts a semantic payload into a plain
// map[string]any that can be fed into asset.CanonicalImageMetadataBuilder.
// It centralises the translation from the infrastructure/semantic
// representation to the canonical image metadata shape.
func semanticPayloadToMap(payload *semantic.Payload) map[string]any {
	if payload == nil {
		return nil
	}

	input := semantic.AssetSemanticInput{
		AssetID:             payload.AssetID,
		PromptOriginal:      payload.PromptOriginal,
		SemanticDescription: payload.SemanticDescription,
		SearchText:          payload.SearchText,
		Subjects:            payload.Subjects,
		Tags:                payload.Tags,
		Categories:          payload.Categories,
		Mood:                payload.Mood,
		Style:               payload.Style,
	}
	out := semantic.BuildAssetMetadata(input, nil)

	if len(payload.ConceptTags) > 0 {
		out["concept_tags"] = payload.ConceptTags
	}
	if len(payload.VisualObjects) > 0 {
		out["visual_objects"] = payload.VisualObjects
	}
	if len(payload.EmotionalTone) > 0 {
		out["emotional_tone"] = payload.EmotionalTone
	}
	if payload.RetrievalScore != nil {
		out["retrieval_score"] = *payload.RetrievalScore
	}

	return out
}
