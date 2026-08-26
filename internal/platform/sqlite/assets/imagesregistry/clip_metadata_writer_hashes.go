// Package assets — clip_metadata_writer_hashes.go: deterministic
// content-hash + event-key helpers extracted from
// clip_metadata_writer.go as part of the file split (July 2026).
//
// godlike/06 SSOT: the two helper functions here are the SINGLE
// canonical surface for content-hash + outbox-event-key derivation
// consumed by the ClipMetadataWriterAdapter entry points in
// clip_metadata_writer.go.
//
// Companion files:
//   - clip_metadata_writer.go (canonical) — owns the
//     ClipMetadataWriterAdapter CONCRETE type + constructor +
//     the two public entry points + the Pattern 0 compile-time
//     pin.
//   - clip_metadata_writer_payload.go — owns
//     updateMediaAssetsMetadataTx, buildMetadataPayload,
//     upsertTextTracksInTx.
//
// Why event-key goes here too (vs. payload): the event_key is
// derived FROM the file hash (file_hash → content_hash →
// source_version → event_key chain) and from the clip ID, so it
// belongs with the other deterministic-hashing functions rather
// than with the JSON payload serialization. Two readers of
// BuildMetadataEventKey (the canonical entry points + the
// buildMetadataPayload cross-reference) both already cross file
// boundaries via package-level symbol sharing, so there is no
// new dependency surface.
package imagesregistry

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ComputeIndexRevision derives the canonical index revision — the
// fingerprint the supersede gate compares to decide whether an
// asset.index.requested event is still current.
//
// It folds BYTE identity (contentHash) with the indexable text-track
// surface so the revision changes when a translation is added or
// corrected, even when the MP4 bytes are unchanged. It does NOT mutate
// contentHash: content_sha256 remains pure byte identity (godlike/06 —
// content_sha256 vs index_revision vs semantic_document_hash are
// distinct fingerprints and MUST NOT be conflated).
//
// Formula: SHA256(contentHash + "|" + sorted(text_track_hashes))
// where sorted means ascending by (language_code, text_kind).
// When no text tracks exist, the revision is just contentHash
// (matching the byte-identity-only snapshot).
func ComputeIndexRevision(contentHash string, textTracks []detail.TextTrack) string {
	if len(textTracks) == 0 {
		return contentHash
	}

	// Sort by (language_code, text_kind) for determinism.
	sorted := make([]detail.TextTrack, len(textTracks))
	copy(sorted, textTracks)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].LanguageCode != sorted[j].LanguageCode {
			return sorted[i].LanguageCode < sorted[j].LanguageCode
		}
		return sorted[i].TextKind < sorted[j].TextKind
	})

	var b strings.Builder
	b.WriteString(contentHash)
	b.WriteString("|")
	for _, t := range sorted {
		if t.TextHash != "" {
			b.WriteString(t.TextHash)
			b.WriteString(";")
		}
	}

	h := digest.SHA256Bytes([]byte(b.String()))
	return h
}

// ComputeContentHashWithTextTracks is the legacy alias for
// ComputeIndexRevision. Deprecated: content_sha256 is BYTE identity and
// must never fold text-track/metadata changes. New callers MUST use
// ComputeIndexRevision; this alias is retained only for the existing
// e2e determinism tests and the facade re-export surface.
func ComputeContentHashWithTextTracks(fileHash string, textTracks []detail.TextTrack) string {
	return ComputeIndexRevision(fileHash, textTracks)
}

// BuildMetadataEventKey returns the canonical event_key for the
// metadata write. Shape:
//
//	metadata:reindex:<clipID>:<sourceVersion>
//
// Different from the Step 9 ClipAtomicWriter's event_key
// (`reconcile:reindex:<clipID>:<schema>:<sourceVersion>`) so the
// outbox dispatcher treats the two events as distinct (the
// metadata event triggers a Qdrant re-index, the clip event
// triggers a fresh insert).
func BuildMetadataEventKey(clipID, sourceVersion string) string {
	if sourceVersion == "" {
		// Empty sourceVersion is fail-closed at the builder
		// level; the writer surfaces an empty event_key as
		// a defensive marker so the dispatcher can detect
		// the bug rather than silently produce a
		// deterministic-but-misleading key.
		return fmt.Sprintf("metadata:reindex:%s:nosource", clipID)
	}
	return fmt.Sprintf("metadata:reindex:%s:%s", clipID, sourceVersion)
}
