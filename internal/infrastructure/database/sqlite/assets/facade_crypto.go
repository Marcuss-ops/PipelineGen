// Package assets — facade_crypto.go. Thin re-export surface
// over the assets/crypto subzone so the 162 external consumers
// (cmd/*, internal/api/*, internal/app/*, internal/application/*,
// internal/domain/*, internal/infrastructure/*, tests/*) of
// `internal/infrastructure/database/sqlite/assets` continue to
// resolve the formerly-flat-package symbols unchanged.
//
// Source of truth for the implementations lives in:
//
//	assets/crypto/clip_metadata_writer_hashes.go
//
// Wrappers below keep:
//   - the package-level import path stable (`...sqlite/assets`),
//   - the exported function names stable for external callers,
//
// AND additionally expose the formerly-unexported
// `buildMetadataEventKey` as `BuildMetadataEventKey` so the
// intra-assets callers (clip_metadata_writer.go,
// clip_metadata_writer_payload.go) can still reach it after the
// subpackage split (July 2026, PR-SQLITE-CRYPTO-SPLIT).
package assets

import (
	sqcrypto "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/crypto"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ComputeContentHashWithTextTracks computes a deterministic content
// hash that includes text track hashes (canonical implementation
// lives in assets/crypto/clip_metadata_writer_hashes.go).
//
// Formula: SHA256(file_hash + "|" + sorted(text_track_hashes))
// where sorted means ascending by (language_code, text_kind).
// When no text tracks exist, the hash is just SHA256(file_hash)
// which matches the existing content_hash behavior.
func ComputeContentHashWithTextTracks(fileHash string, textTracks []asset.TextTrack) string {
	return sqcrypto.ComputeContentHashWithTextTracks(fileHash, textTracks)
}

// BuildMetadataEventKey returns the canonical event_key for the
// metadata write.
//
// Shape:
//
//	metadata:reindex:<clipID>:<sourceVersion>
//
// Renamed from unexported `buildMetadataEventKey` at the assets/
// → assets/crypto split to enable cross-package access from
// clip_metadata_writer.go + clip_metadata_writer_payload.go
// (both still in this assets/ root). Empty sourceVersion is
// fail-closed at the builder level; the writer surfaces an empty
// event_key as a defensive marker so the dispatcher can detect
// the bug rather than silently produce a
// deterministic-but-misleading key.
func BuildMetadataEventKey(clipID, sourceVersion string) string {
	return sqcrypto.BuildMetadataEventKey(clipID, sourceVersion)
}
