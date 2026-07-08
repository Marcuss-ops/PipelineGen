// Package indexing — payload_builder.go: canonical writer-side payload builder.
//
// Extracted from payload_mapper_document.go (July 2026).
// Owns: BuildPayloadFromDocument.
package indexing

import (
	"fmt"
	"strconv"
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
// FORBIDDEN at this emitter: drive_link, local_path, status payload
// keys. The AirLock via IndexDocument strips these from the wire shape;
// this fn preserves the invariant. The freeze test in
// composition_test.go::TestComposition_FrozenQdrantIndexDocumentCanonicalTypes
// + the wire-level test below pin both halves of the invariant.
func BuildPayloadFromDocument(doc *IndexDocument, schema *schema.IndexSchema) map[string]interface{} {
	if doc == nil {
		return map[string]interface{}{}
	}
	semanticTitle := doc.Metadata.SemanticTitle
	if semanticTitle == "" {
		semanticTitle = buildSemanticTitle(doc.Metadata.Title, doc.Metadata.Event, doc.Metadata.Round, doc.Metadata.Scene, doc.Metadata.Subject)
	}
	embeddingText := doc.Metadata.EmbeddingText
	if embeddingText == "" {
		embeddingText = buildEmbeddingText(doc, semanticTitle)
	}
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
	payload := map[string]interface{}{
		"asset_id":        doc.AssetID,
		"lifecycle_state": string(doc.LifecycleState),
		"source":          doc.Metadata.Source,
		"media_type":      doc.Metadata.MediaType,
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
	if doc.Metadata.IndexingStatus != "" {
		payload["indexing_status"] = doc.Metadata.IndexingStatus
	}
	// PR-TIMESTAMP-FOLDER-LINK (July 2026): parent timestamp Drive
	// folder metadata for "open in Drive" navigation from search
	// results. drive_link remains FORBIDDEN (QDRANT-001); these are
	// distinct keys with distinct semantics (folder vs file).
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

func buildEmbeddingText(doc *IndexDocument, semanticTitle string) string {
	if doc == nil {
		return ""
	}
	parts := make([]string, 0, 12)
	if semanticTitle != "" {
		parts = append(parts, semanticTitle)
	}
	if doc.Metadata.Summary != "" && doc.Metadata.Summary != semanticTitle {
		parts = append(parts, doc.Metadata.Summary)
	}
	if doc.Metadata.Description != "" {
		parts = append(parts, doc.Metadata.Description)
	}
	if doc.Metadata.Hook != "" {
		parts = append(parts, "hook: "+doc.Metadata.Hook)
	}
	if doc.Metadata.Event != "" {
		parts = append(parts, "event: "+doc.Metadata.Event)
	}
	if doc.Metadata.Scene != "" {
		parts = append(parts, "scene: "+doc.Metadata.Scene)
	}
	if doc.Metadata.Subject != "" {
		parts = append(parts, "subject: "+doc.Metadata.Subject)
	}
	if doc.Metadata.SourceProvider != "" || doc.Metadata.Source != "" {
		source := firstNonEmpty(doc.Metadata.SourceProvider, doc.Metadata.Source)
		if source != "" {
			parts = append(parts, "source: "+source)
		}
	}
	if doc.Metadata.Origin != "" {
		parts = append(parts, "origin: "+doc.Metadata.Origin)
	}
	if doc.Metadata.Destination != "" {
		parts = append(parts, "destination: "+doc.Metadata.Destination)
	}
	if doc.Metadata.SourceURL != "" {
		parts = append(parts, "source_url: "+doc.Metadata.SourceURL)
	}
	if doc.Metadata.SourceVideoID != "" {
		parts = append(parts, "source_video_id: "+doc.Metadata.SourceVideoID)
	}
	if doc.Metadata.PolicyVersion != "" {
		parts = append(parts, "policy_version: "+doc.Metadata.PolicyVersion)
	}
	if doc.Metadata.JobID != "" {
		parts = append(parts, "job_id: "+doc.Metadata.JobID)
	}
	if doc.Metadata.WorkflowID != "" {
		parts = append(parts, "workflow_id: "+doc.Metadata.WorkflowID)
	}
	if doc.Metadata.RunFingerprint != "" {
		parts = append(parts, "run_fingerprint: "+doc.Metadata.RunFingerprint)
	}
	if doc.Metadata.ChunkIndex > 0 || doc.Metadata.TotalChunks > 0 {
		parts = append(parts, "chunk_index: "+strconv.Itoa(doc.Metadata.ChunkIndex))
	}
	if doc.Metadata.TotalChunks > 0 {
		parts = append(parts, "total_chunks: "+strconv.Itoa(doc.Metadata.TotalChunks))
	}
	if doc.Metadata.Category != "" {
		parts = append(parts, "category: "+doc.Metadata.Category)
	}
	if doc.Metadata.Language != "" {
		parts = append(parts, "language: "+doc.Metadata.Language)
	}
	if len(doc.Metadata.Topics) > 0 {
		parts = append(parts, "topics: "+strings.Join(doc.Metadata.Topics, ", "))
	}
	if len(doc.Metadata.Speakers) > 0 {
		parts = append(parts, "speakers: "+strings.Join(doc.Metadata.Speakers, ", "))
	}
	if len(doc.Metadata.MentionedPeople) > 0 {
		parts = append(parts, "mentioned_people: "+strings.Join(doc.Metadata.MentionedPeople, ", "))
	}
	if len(doc.Metadata.Entities) > 0 {
		parts = append(parts, "entities: "+strings.Join(doc.Metadata.Entities, ", "))
	}
	if len(doc.Metadata.SearchKeywords) > 0 {
		parts = append(parts, "search_keywords: "+strings.Join(doc.Metadata.SearchKeywords, ", "))
	}
	if len(doc.Metadata.Tags) > 0 {
		parts = append(parts, "tags: "+strings.Join(doc.Metadata.Tags, ", "))
	}
	if doc.Metadata.DurationSec > 0 {
		parts = append(parts, "duration_sec: "+strconv.Itoa(doc.Metadata.DurationSec))
	}
	if doc.SearchText != "" {
		parts = append(parts, "search_text: "+doc.SearchText)
	}
	return strings.Join(parts, ". ")
}
