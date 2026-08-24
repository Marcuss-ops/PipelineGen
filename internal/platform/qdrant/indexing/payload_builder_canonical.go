// Package indexing — payload_builder_canonical.go: canonical SSOT block
// + text-track projection keys for the canonical writer-side payload
// builder.
//
// Extracted from payload_builder.go::BuildPayloadFromDocument (July 2026
// domain split). Owns: fillCanonicalPayload (drive_file_id / drive_link /
// current_semantic_hash + YouTube source identity) and
// fillTextTrackPayload (language availability projection).
package indexing

// fillCanonicalPayload writes the canonical payload block
// (PR-CATALOG-MULTILINGUA step 6): drive_file_id, drive_link,
// current_semantic_hash, the source video identity and description.
// sourceVideoID is pre-resolved by the orchestrator via firstNonEmpty.
func fillCanonicalPayload(payload map[string]any, doc *IndexDocument, sourceVideoID string) {
	// ── Canonical payload block (PR-CATALOG-MULTILINGUA step 6, July 2026).
	// These 2 keys are the search-result-key payload for the multilingual
	// clip catalog. drive_link replaces the legacy QDRANT-001 NO-DRIVE-LINK
	// rule (Italian plan: drive_link is canonical in payload — but NEVER
	// in embedding_text; the forward-prevention test
	// TestBuildPayloadFromDocument_CanonicalSearchDocument_NoLinkOrLocatorInEmbeddingText
	// pins that half of the invariant).
	// current_semantic_hash is the SSOT fingerprint that gates the upsert
	// supersede when VLM/translations mutate without content_hash changing.
	// Precedence rule (godlike/06 SSOT — AssetStore is the canonical
	// owner of the resolved value; airlock layers just trust it):
	//   (a) asset_visual_summaries.source_hash (VLM fingerprint,
	//       migration 151) — populated post a real VLM pass.
	//   (b) media_assets.semantic_hash (migration 152) — fallback.
	// The AssetStore layer wires AssetData.SemanticHash from the
	// JOIN vs ∪ ma precedence (follow-up PR; the airlock trusts the
	// pre-resolved value).
	// godlike/07 NO-FAKE-AVAILABILITY: both keys are omitempty; absence
	// means the producer side hasn't populated them yet.
	if doc.Metadata.DriveFileID != "" {
		payload["drive_file_id"] = doc.Metadata.DriveFileID
	}
	if doc.Metadata.DriveLink != "" {
		payload["drive_link"] = doc.Metadata.DriveLink
	}
	if doc.Metadata.CurrentSemanticHash != "" {
		payload["current_semantic_hash"] = doc.Metadata.CurrentSemanticHash
	}
	if sourceVideoID != "" {
		payload["source_video_id"] = sourceVideoID
	}
	if doc.Metadata.YouTubeID != "" {
		payload["youtube_video_id"] = doc.Metadata.YouTubeID
	}
	if doc.Metadata.YouTubeURL != "" {
		payload["youtube_url"] = doc.Metadata.YouTubeURL
	}
	if doc.Metadata.Description != "" {
		payload["description"] = doc.Metadata.Description
	}
}

// fillTextTrackPayload writes the lightweight text-track projection
// (no full transcripts — only availability + language surface).
func fillTextTrackPayload(payload map[string]any, doc *IndexDocument) {
	// ── Text track projection (lightweight, no full transcripts) ──
	if doc.Metadata.OriginalLanguage != "" {
		payload["original_language"] = doc.Metadata.OriginalLanguage
	}
	if len(doc.Metadata.AvailableLanguages) > 0 {
		payload["available_languages"] = doc.Metadata.AvailableLanguages
	}
	if doc.Metadata.TranscriptAvailable {
		payload["transcript_available"] = true
	}
	if doc.Metadata.TextTracksVersion != "" {
		payload["text_tracks_version"] = doc.Metadata.TextTracksVersion
	}
}
