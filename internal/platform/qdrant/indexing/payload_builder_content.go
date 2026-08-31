// Package indexing — payload_builder_content.go: content metadata +
// provenance / versioning payload keys for the canonical writer-side
// payload builder.
//
// Extracted from payload_builder.go::BuildPayloadFromDocument (July 2026
// domain split). Owns: fillContentPayload — language/classification
// metadata, durations, index/policy/source versions, job provenance,
// chunking, Drive folder location, timestamps, and the per-channel
// embedding_version_* provenance loop.
package indexing

import "fmt"

// fillContentPayload writes the content metadata, provenance / versioning,
// Drive location and timestamp keys, plus the per-channel embedding
// version loop. Guards moved verbatim from the pre-split emitter.
func fillContentPayload(payload map[string]any, doc *IndexDocument) {
	// content_hash is the canonical SQLite content identity. It is
	// projected verbatim so ReconcileProjection can compare Qdrant
	// without ever treating Qdrant as the source of truth.
	if doc.ContentHash != "" {
		payload["content_hash"] = doc.ContentHash
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
		if artifact.Model != "" {
			payload[fmt.Sprintf("embedding_model_%s", string(channel))] = artifact.Model
		}
		if artifact.ModelVersion == "" {
			continue
		}
		payload[fmt.Sprintf("embedding_version_%s", string(channel))] = artifact.ModelVersion
	}
}
