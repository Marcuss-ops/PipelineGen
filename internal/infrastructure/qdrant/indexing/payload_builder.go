// Package indexing — payload_builder.go: canonical writer-side payload builder.
//
// Extracted from payload_mapper_document.go (July 2026).
// Owns: BuildPayloadFromDocument.
package indexing

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// ══════════════════════════════════════════════════════════════════════════
// PR 6 (refactor/qdrant-index-document) — canonical Mapper airlock.
// ══════════════════════════════════════════════════════════════════════════

// BuildPayloadFromDocument is the canonical writer-side payload builder.
// Reads an IndexDocument (defined in index_document.go) and emits the
// canonical Qdrant payload.
//
// PR 6 (verdict §11): the per-channel embedding_version_<channel>
// payload keys are written from doc.Embeddings[channel].ModelVersion
// (the OBSERVED provenance recorded in the artifact), NOT from
// schema.DenseVectors.ModelVersion (the schema's EXPECTED). The schema
// declares the contract the operator wants; the artifact records what
// the writer actually emitted. Drift surface: the per-channel counter
// in schema.SwitchReport.VersionMismatchPerChannel (PR 12).
//
// FORBIDDEN at this emitter: local_path, status payload keys (locked by
// the IndexDocument airlock — neither field exists on IndexDocument).
// drive_link IS now canonical in payload (PR-CATALOG-MULTILINGUA step
// 6, July 2026) but is FORBIDDEN in embedding_text (forward-prevention
// test
// TestBuildPayloadFromDocument_CanonicalSearchDocument_NoLinkOrLocatorInEmbeddingText
// pins that half of the invariant). The freeze test in
// composition_test.go::TestComposition_FrozenQdrantIndexDocumentCanonicalTypes
// pins the forbidden-SSOT half.
func BuildPayloadFromDocument(doc *IndexDocument, schema *schema.IndexSchema) map[string]any {
	if doc == nil {
		return map[string]any{}
	}
	semanticTitle := doc.Metadata.SemanticTitle
	if semanticTitle == "" {
		semanticTitle = buildSemanticTitle(doc.Metadata.Title, doc.Metadata.Event, doc.Metadata.Round, doc.Metadata.Scene, doc.Metadata.Subject)
	}
	// PR-CATALOG-MULTILINGUA step 6 (July 2026): the canonical SEARCH
	// DOCUMENT is ALWAYS composed via buildCanonicalSearchDocument;
	// the pre-existing pre-fill override
	// (`if doc.Metadata.EmbeddingText != ""`) is REMOVED so no caller
	// can bypass the forward-prevention gate by sneaking
	// link/locator text into doc.Metadata.EmbeddingText. The
	// canonical composer reads ONLY the 8 sanctioned fields and
	// emits an empty string when none are populated; that is the
	// SINGLE source of truth for the embedding shape on the wire.
	embeddingText := buildCanonicalSearchDocument(doc)
	sourceURL := firstNonEmpty(doc.Metadata.SourceURL, doc.Metadata.YouTubeURL)
	sourceVideoID := firstNonEmpty(doc.Metadata.SourceVideoID, doc.Metadata.YouTubeID)
	name := firstNonEmpty(doc.Metadata.Name, doc.Metadata.Title, doc.Metadata.SemanticTitle, doc.AssetID)
	// PR 6 (July 2026): removed the legacy fall-back to `doc.Metadata.Source`
	// for destination / source_provider / origin. The pre-PR-6 builder
	// silently filled these from asset.Source as a "make sure we have
	// SOMETHING" default — exactly the godlike/07 NO-FAKE-AVAILABILITY
	// placeholder-string anti-pattern. Callers that want these fields MUST
	// set them explicitly via top-level AssetData fields or MetadataJSON.
	destination := doc.Metadata.Destination
	origin := doc.Metadata.Origin
	sourceProvider := doc.Metadata.SourceProvider
	entities := mergeStringSlices(doc.Metadata.Entities, doc.Metadata.People, doc.Metadata.Speakers, doc.Metadata.MentionedPeople)
	if len(entities) == 0 {
		entities = mergeStringSlices(doc.Metadata.People, doc.Metadata.Topics, doc.Metadata.SearchKeywords)
	}
	payload := map[string]any{
		"asset_id":        doc.AssetID,
		"lifecycle_state": string(doc.LifecycleState),
		"source":          doc.Metadata.Source,
		"media_type":      doc.Metadata.MediaType,
	}
	if doc.Metadata.AssetRole != "" {
		payload["asset_role"] = doc.Metadata.AssetRole
	}
	if doc.Metadata.NormalizedGroup != "" {
		payload["normalized_group"] = doc.Metadata.NormalizedGroup
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
	if doc.Metadata.Language != "" {
		payload["language"] = doc.Metadata.Language
	}
	if doc.Metadata.Category != "" {
		payload["category"] = doc.Metadata.Category
	}
	if doc.Metadata.Style != "" {
		payload["style"] = doc.Metadata.Style
	}
	if doc.Metadata.YouTubeChan != "" {
		payload["channel_id"] = doc.Metadata.YouTubeChan
	}
	if doc.Metadata.Summary != "" {
		payload["summary"] = doc.Metadata.Summary
	}
	if doc.Metadata.License != "" {
		payload["license"] = doc.Metadata.License
	}
	if doc.Metadata.DurationMs > 0 {
		payload["duration_ms"] = doc.Metadata.DurationMs
	}
	if doc.Metadata.DurationSec > 0 {
		payload["duration_sec"] = doc.Metadata.DurationSec
	}
	if doc.Metadata.IndexVersion != "" {
		payload["index_version"] = doc.Metadata.IndexVersion
	}
	if doc.Metadata.PolicyVersion != "" {
		payload["policy_version"] = doc.Metadata.PolicyVersion
	}
	if doc.SourceVersion != "" {
		payload["source_version"] = doc.SourceVersion
	}
	if doc.Metadata.JobID != "" {
		payload["job_id"] = doc.Metadata.JobID
	}
	if doc.Metadata.WorkflowID != "" {
		payload["workflow_id"] = doc.Metadata.WorkflowID
	}
	if doc.Metadata.RunFingerprint != "" {
		payload["run_fingerprint"] = doc.Metadata.RunFingerprint
	}
	if doc.Metadata.ChunkIndex > 0 || doc.Metadata.TotalChunks > 0 || doc.Metadata.JobID != "" || doc.Metadata.WorkflowID != "" || doc.Metadata.RunFingerprint != "" {
		payload["chunk_index"] = doc.Metadata.ChunkIndex
	}
	if doc.Metadata.TotalChunks > 0 {
		payload["total_chunks"] = doc.Metadata.TotalChunks
	}
	if doc.Metadata.DrivePath != "" {
		payload["drive_path"] = doc.Metadata.DrivePath
	}
	if doc.Metadata.FolderID != "" {
		payload["folder_id"] = doc.Metadata.FolderID
	}
	if doc.Metadata.FolderPath != "" {
		payload["folder_path"] = doc.Metadata.FolderPath
	}
	if doc.Metadata.IndexingStatus != "" {
		payload["indexing_status"] = doc.Metadata.IndexingStatus
	}
	// PR-TIMESTAMP-FOLDER-LINK (July 2026): parent timestamp Drive
	// folder metadata for "open in Drive" navigation from search
	// results. drive_link is the file-level canonical URL emitted
	// below; the timestamp_* keys are FOLDER-level distinct surfaces
	// (folder navigation vs file playback).
	if doc.Metadata.TimestampDriveFolderLink != "" {
		payload["timestamp_drive_folder_link"] = doc.Metadata.TimestampDriveFolderLink
	}
	if doc.Metadata.TimestampFolderID != "" {
		payload["timestamp_folder_id"] = doc.Metadata.TimestampFolderID
	}
	if doc.Metadata.CreatedAt != "" {
		payload["created_at"] = doc.Metadata.CreatedAt
	}
	if doc.Metadata.UpdatedAt != "" {
		payload["updated_at"] = doc.Metadata.UpdatedAt
	}
	if doc.Metadata.DeletedAt != "" {
		payload["deleted_at"] = doc.Metadata.DeletedAt
	}

	// Provenance WINS over schema (verdict §11). Per-artifact observed
	// version keys the on-disk payload. Empty observed versions are
	// SKIPPED so the verifier's per-channel counter surfaces the gap
	// loudly (non-silent failure) — a future PR that sources
	// observed-from-metadata_json will populate ModelVersion here and
	// the wire matches the source-of-truth automatically.
	for channel, artifact := range doc.Embeddings {
		if artifact.ModelVersion == "" {
			continue
		}
		payload[fmt.Sprintf("embedding_version_%s", string(channel))] = artifact.ModelVersion
	}

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

	// ── VLM visual summary block (FASE-9 + visual-summary reindex) ────
	// Six keys, each guarded by an omitempty contract per the strict-emit
	// test pattern (mirror of `lifecycle_state` / `embedding_version_*`).
	// godlike/07 NO-FAKE-AVAILABILITY: a missing VLM pass MUST omit the
	// keys entirely (NOT "" / NOT []), so the Qdrant reader sees
	// Apache Arrow "missing field" semantics — ReindexVerifier reports
	// drift on the post-reindex cross-check.
	//
	// godlike/06 SSOT: the canonical payload-key naming for the VLM
	// block is documented in migrations/sqlite/151_asset_visual_summaries.sql
	// (the migration's package-level doctrine, the visual-summary
	// reindex CLI, and the Qdrant ReindexVerifier all read this list).
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

	return payload
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

// buildCanonicalSearchDocument composes the canonical search text for
// the asset per PR-CATALOG-MULTILINGUA step 6 (July 2026).
//
// The canonical 8-field composition is:
//
//  1. title            ← doc.Metadata.Title
//  2. description      ← doc.Metadata.Description
//  3. visual_summary   ← doc.Metadata.VisualSummary
//  4. transcript       ← doc.Metadata.Transcripts (multilingual joined;
//     original-language row bare, sequels as `transcript ({lang}): …`)
//     with document.Metadata.Transcript (single string) as the v1
//     fallback for callers that haven't yet adopted the TextTrackQuerier
//     pipeline.
//  5. topics           ← doc.Metadata.Topics (joined as "topics: a, b, c")
//  6. entities         ← doc.Metadata.Entities (joined as "entities: a, b, c")
//  7. event            ← doc.Metadata.Event
//  8. scene            ← doc.Metadata.Scene
//
// Empty fields are SKIPPED (godlike/07 NO-FAKE-AVAILABILITY: a missing
// field MUST NOT emit a placeholder line). Each non-empty field is on
// its own line so the operator can eyeball-verify the composer output.
//
// Multimodal transcript assembly (PR-CATALOG-MULTILINGUA step 6):
// the slice asset.Transcripts carries one row per `is_current=1`
// transcript in asset_text_tracks. The composer:
//   - emits the IsOriginal row (matched against
//     doc.Metadata.OriginalLanguage via case-folded equality) as BARE
//     text on its own line — the primary embedding-text signal.
//   - emits each non-original row as
//     `transcript ({Lang}): {Text}` on a new line — language-coded
//     sequels. Deterministic order: original row first (if any), then
//     non-original rows in Lang-ASC alphabetical order so re-runs
//     produce byte-stable embedding_text.
//
// Forward-prevention contract (pinned by payload_builder_test.go):
// the composition MUST NOT contain any link/locator metadata. The
// following forbidden substrings — if any appear in the output —
// trigger the test failure surface:
//
//	drive_link / drive_path / source_url / http:// / https://
//	source_video_id / youtube_video_id / youtube_url
//	job_id / workflow_id / policy_version / chunk_index / total_chunks
//	run_fingerprint / chunk_id / clip_id / asset_id
//
// godlike/06 SSOT: the canonical field set is the SINGLE source of
// truth for the embedding_text composition. Per-source variations
// (YouTube additional hook, Stock tags formatted as "Tags: ...")
// belong in payload filter fields, NOT in the embedding_text. The
// embedding_text is the search vector input — link/locator in it
// dilutes the embedding's semantic focus.
//
// godlike/07 minimum-blast-radius: the pre-existing per-source
// switch-case helper (which embedded workflow_id, job_id, run_fingerprint,
// chunk_index, total_chunks, policy_version, source_video_id, source_provider
// labels) was REMOVED in this PR alongside the strconv import — that
// import was only used by that helper. The 8-field composer is the
// ONLY embedding_text composer in the file.
func buildCanonicalSearchDocument(doc *IndexDocument) string {
	if doc == nil {
		return ""
	}
	parts := make([]string, 0, 8)

	if v := strings.TrimSpace(doc.Metadata.Title); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(doc.Metadata.Description); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(doc.Metadata.VisualSummary); v != "" {
		parts = append(parts, v)
	}
	if transcript := composeMultilingualTranscriptBlock(doc); transcript != "" {
		parts = append(parts, transcript)
	}
	if len(doc.Metadata.Topics) > 0 {
		parts = append(parts, "topics: "+strings.Join(doc.Metadata.Topics, ", "))
	}
	if len(doc.Metadata.Entities) > 0 {
		parts = append(parts, "entities: "+strings.Join(doc.Metadata.Entities, ", "))
	}
	if v := strings.TrimSpace(doc.Metadata.Event); v != "" {
		parts = append(parts, "event: "+v)
	}
	if v := strings.TrimSpace(doc.Metadata.Scene); v != "" {
		parts = append(parts, "scene: "+v)
	}
	return strings.Join(parts, "\n")
}

// composeMultilingualTranscriptBlock renders the multilingual transcript
// canon for embedding_text per PR-CATALOG-MULTILINGUA step 6. Three cases:
//
//  1. doc.Metadata.Transcripts is non-empty: emit IsOriginal row as bare
//     text first, then each non-original row as
//     `transcript ({Lang}): {Text}` on a new line. Rows with empty
//     Text are SKIPPED (godlike/07 NO-FAKE-AVAILABILITY). Lang code
//     is case-folded for the original-language match so `"EN"` and
//     `"en"` are equivalent. Order within the sequels is Lang-ASC
//     alphabetical for byte-stable output.
//  2. doc.Metadata.Transcripts is empty AND doc.Metadata.Transcript
//     is non-empty (legacy single-string fallback): emit
//     doc.Metadata.Transcript verbatim. This keeps the embedding-text
//     stable for the transition window for callers that haven't yet
//     adopted the new TextTrackQuerier flow.
//  3. Both empty: emit "" (the embedding_text composer at the caller
//     layer SKIPS the field — embedding-text just doesn't include the
//     transcript slot).
func composeMultilingualTranscriptBlock(doc *IndexDocument) string {
	if doc == nil {
		return ""
	}
	if len(doc.Metadata.Transcripts) > 0 {
		originalLang := strings.TrimSpace(doc.Metadata.OriginalLanguage)
		var originalLine string
		var others []TranscriptTrack
		for _, t := range doc.Metadata.Transcripts {
			text := strings.TrimSpace(t.Text)
			if text == "" {
				continue // godlike/07 NO-FAKE-AVAILABILITY: skip empty rows
			}
			if originalLang != "" && strings.EqualFold(t.Lang, originalLang) && t.IsOriginal {
				originalLine = text
				continue
			}
			if t.IsOriginal && originalLang == "" {
				// OriginalLanguage empty on the airlock but IsOriginal=true
				// (text_track_repository wires IsOriginal from media_assets.language;
				// if that is empty, ANY row can claim IsOriginal). Treat the
				// FIRST IsOriginal row as bare text and route subsequent rows
				// to the sequel bucket so a single bare-text slot is emitted.
				if originalLine == "" {
					originalLine = text
					continue
				}
			}
			others = append(others, TranscriptTrack{Lang: t.Lang, Text: text})
		}
		// Sort sequels by Lang ASC for deterministic byte-stable output.
		sort.SliceStable(others, func(i, j int) bool {
			return others[i].Lang < others[j].Lang
		})
		out := ""
		if originalLine != "" {
			out = originalLine
			for _, t := range others {
				out += "\ntranscript (" + t.Lang + "): " + t.Text
			}
			return out
		}
		// No IsOriginal row identified. Emit ALL rows as sequels with a
		// stable Lang-ASC order so the rubric stays non-empty.
		for _, t := range others {
			if out == "" {
				out = "transcript (" + t.Lang + "): " + t.Text
			} else {
				out += "\ntranscript (" + t.Lang + "): " + t.Text
			}
		}
		return out
	}
	// Legacy single-string fallback (pre-step-6 callers).
	return strings.TrimSpace(doc.Metadata.Transcript)
}
