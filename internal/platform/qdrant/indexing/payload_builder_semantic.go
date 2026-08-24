// Package indexing — payload_builder_semantic.go: semantic content keys
// for the canonical writer-side payload builder.
//
// Extracted from payload_builder.go::BuildPayloadFromDocument (July 2026
// domain split). Owns: fillSemanticPayload — title/event/round/scene/
// subject, topics/speakers/people, tags, entities, search visibility and
// free-text search surface keys.
package indexing

// fillSemanticPayload writes the title/event/scene block, people/topics
// tags, entities (pre-resolved by the orchestrator) and the search
// surface keys. Guards moved verbatim from the pre-split emitter.
func fillSemanticPayload(payload map[string]any, doc *IndexDocument, entities []string) {
	if doc.Metadata.Title != "" {
		payload["title"] = doc.Metadata.Title
	}
	if doc.Metadata.Event != "" {
		payload["event"] = doc.Metadata.Event
	}
	if doc.Metadata.Round > 0 {
		payload["round"] = doc.Metadata.Round
	}
	if doc.Metadata.Scene != "" {
		payload["scene"] = doc.Metadata.Scene
	}
	if doc.Metadata.Subject != "" {
		payload["subject"] = doc.Metadata.Subject
	}
	if len(doc.Metadata.Topics) > 0 {
		payload["topics"] = doc.Metadata.Topics
	}
	if len(doc.Metadata.Speakers) > 0 {
		payload["speakers"] = doc.Metadata.Speakers
	}
	if len(doc.Metadata.MentionedPeople) > 0 {
		payload["mentioned_people"] = doc.Metadata.MentionedPeople
	}
	if len(doc.Metadata.People) > 0 {
		payload["people"] = doc.Metadata.People
	}
	if len(doc.Metadata.SourceTags) > 0 {
		payload["source_tags"] = doc.Metadata.SourceTags
	}
	if len(doc.Metadata.ClipTags) > 0 {
		payload["clip_tags"] = doc.Metadata.ClipTags
	}
	if len(doc.Metadata.SearchKeywords) > 0 {
		payload["search_keywords"] = doc.Metadata.SearchKeywords
	}
	if len(entities) > 0 {
		payload["entities"] = entities
	}
	if doc.Metadata.SearchVisibility != "" {
		payload["search_visibility"] = doc.Metadata.SearchVisibility
	}
	if doc.Metadata.Hook != "" {
		payload["hook"] = doc.Metadata.Hook
	}
	if len(doc.Metadata.Tags) > 0 {
		payload["tags"] = doc.Metadata.Tags
	}
	if doc.SearchText != "" {
		payload["search_text"] = doc.SearchText
	}
}
