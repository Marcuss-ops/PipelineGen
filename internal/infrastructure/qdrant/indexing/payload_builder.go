// Package indexing — payload_builder.go: canonical writer-side payload builder.
//
// Extracted from payload_mapper_document.go (July 2026).
// Owns: BuildPayloadFromDocument.
package indexing

import (
	"fmt"

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
	payload := map[string]interface{}{
		"asset_id":        doc.AssetID,
		"lifecycle_state": string(doc.LifecycleState),
		"source":          doc.Metadata.Source,
		"media_type":      doc.Metadata.MediaType,
	}

	if doc.WorkspaceID != "" {
		payload["workspace_id"] = doc.WorkspaceID
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
	if doc.Metadata.License != "" {
		payload["license"] = doc.Metadata.License
	}
	if doc.Metadata.DurationMs > 0 {
		payload["duration_ms"] = doc.Metadata.DurationMs
	}
	if doc.Metadata.IndexVersion != "" {
		payload["index_version"] = doc.Metadata.IndexVersion
	}
	if doc.SourceVersion != "" {
		payload["source_version"] = doc.SourceVersion
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
	if len(doc.Metadata.Tags) > 0 {
		payload["tags"] = doc.Metadata.Tags
	}
	if doc.SearchText != "" {
		payload["search_text"] = doc.SearchText
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
