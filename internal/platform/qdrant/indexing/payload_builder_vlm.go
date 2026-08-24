// Package indexing — payload_builder_vlm.go: VLM visual-summary keys for
// the canonical writer-side payload builder.
//
// Extracted from payload_builder.go::BuildPayloadFromDocument (July 2026
// domain split). Owns: fillVLMPayload — the six VLM visual-summary keys,
// each guarded by an omitempty contract.
package indexing

// fillVLMPayload writes the VLM visual summary block (FASE-9 +
// visual-summary reindex). Six keys, each guarded by an omitempty
// contract per the strict-emit test pattern (mirror of `lifecycle_state`
// / `embedding_version_*`). godlike/07 NO-FAKE-AVAILABILITY: a missing
// VLM pass MUST omit the keys entirely (NOT "" / NOT []), so the Qdrant
// reader sees Apache Arrow "missing field" semantics — ReindexVerifier
// reports drift on the post-reindex cross-check.
//
// godlike/06 SSOT: the canonical payload-key naming for the VLM
// block is documented in migrations/sqlite/151_asset_visual_summaries.sql
// (the migration's package-level doctrine, the visual-summary
// reindex CLI, and the Qdrant ReindexVerifier all read this list).
func fillVLMPayload(payload map[string]any, doc *IndexDocument) {
	if doc.Metadata.VisualSummary != "" {
		payload["visual_summary"] = doc.Metadata.VisualSummary
	}
	if len(doc.Metadata.VisibleActions) > 0 {
		payload["visible_actions"] = doc.Metadata.VisibleActions
	}
	if len(doc.Metadata.VisibleEntities) > 0 {
		payload["visible_entities"] = doc.Metadata.VisibleEntities
	}
	if doc.Metadata.VisualPreprocessingVersion != "" {
		payload["visual_preprocessing_version"] = doc.Metadata.VisualPreprocessingVersion
	}
	if doc.Metadata.VisualModelName != "" {
		payload["visual_model_name"] = doc.Metadata.VisualModelName
	}
	if doc.Metadata.VisualModelVersion != "" {
		payload["visual_model_version"] = doc.Metadata.VisualModelVersion
	}
}
