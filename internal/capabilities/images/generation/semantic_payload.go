package generation

// semanticPayloadToMap converts a semantic payload into a plain map[string]any
// for the canonical image metadata builder.
func semanticPayloadToMap(payload *SemanticPayload) map[string]any {
	if payload == nil {
		return nil
	}
	out := map[string]any{}
	if payload.AssetID != "" {
		out["asset_id"] = payload.AssetID
	}
	if payload.PromptOriginal != "" {
		out["prompt_original"] = payload.PromptOriginal
	}
	if payload.SemanticDescription != "" {
		out["semantic_description"] = payload.SemanticDescription
	}
	if payload.SearchText != "" {
		out["search_text"] = payload.SearchText
	}
	if len(payload.Subjects) > 0 {
		out["subjects"] = payload.Subjects
	}
	if len(payload.Tags) > 0 {
		out["tags"] = payload.Tags
	}
	if len(payload.Categories) > 0 {
		out["categories"] = payload.Categories
	}
	if len(payload.Mood) > 0 {
		out["mood"] = payload.Mood
	}
	if len(payload.Style) > 0 {
		out["style"] = payload.Style
	}
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
