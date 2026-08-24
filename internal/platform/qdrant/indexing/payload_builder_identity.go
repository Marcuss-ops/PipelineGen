// Package indexing — payload_builder_identity.go: base identity +
// classification payload keys for the canonical writer-side payload
// builder.
//
// Extracted from payload_builder.go::BuildPayloadFromDocument (July 2026
// domain split). Owns: fillIdentityPayload — the always-present seed
// keys (asset_id / lifecycle_state / source / media_type) plus the
// guarded classification + locator identity keys.
package indexing

// fillIdentityPayload seeds the payload map with the base identity keys
// and the guarded classification / locator keys. Called by the
// orchestrator (payload_builder.go) with the pre-resolved fallback
// values so the emitted payload is byte-identical to the pre-split flat
// emitter.
func fillIdentityPayload(payload map[string]any, doc *IndexDocument, name, destination, origin, sourceProvider, sourceURL, semanticTitle, embeddingText string) {
	payload["asset_id"] = doc.AssetID
	payload["lifecycle_state"] = string(doc.LifecycleState)
	payload["source"] = doc.Metadata.Source
	payload["media_type"] = doc.Metadata.MediaType
	if doc.Metadata.AssetRole != "" {
		payload["asset_role"] = doc.Metadata.AssetRole
	}
	if doc.Metadata.NormalizedGroup != "" {
		payload["normalized_group"] = doc.Metadata.NormalizedGroup
	}
	// Canonical taxonomy dimensions (migration 195): always emitted when
	// populated so the Qdrant payload carries the full canonical taxonomy
	// (media_type / asset_kind / source_type / namespace / semantic_role).
	if doc.Metadata.Namespace != "" {
		payload["namespace"] = doc.Metadata.Namespace
	}
	if doc.Metadata.AssetKind != "" {
		payload["asset_kind"] = doc.Metadata.AssetKind
	}
	if doc.Metadata.SourceType != "" {
		payload["source_type"] = doc.Metadata.SourceType
	}
	if doc.Metadata.SemanticRole != "" {
		payload["semantic_role"] = doc.Metadata.SemanticRole
	}
	if doc.Metadata.HasDialogue != nil {
		payload["has_dialogue"] = *doc.Metadata.HasDialogue
	}
	if doc.Metadata.AudioProfile != "" {
		payload["audio_profile"] = doc.Metadata.AudioProfile
	}

	if doc.WorkspaceID != "" {
		payload["workspace_id"] = doc.WorkspaceID
	}
	if name != "" {
		payload["name"] = name
	}
	if destination != "" {
		payload["destination"] = destination
	}
	if origin != "" {
		payload["origin"] = origin
	}
	if sourceProvider != "" {
		payload["source_provider"] = sourceProvider
	}
	if sourceURL != "" {
		payload["source_url"] = sourceURL
	}
	if semanticTitle != "" {
		payload["semantic_title"] = semanticTitle
	}
	if embeddingText != "" {
		payload["embedding_text"] = embeddingText
	}
}
