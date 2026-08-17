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

// ComputeIndexRevision derives the canonical index revision — the
// fingerprint the supersede gate compares (canonical implementation
// lives in assets/crypto/clip_metadata_writer_hashes.go). It folds
// byte identity + text-track content WITHOUT mutating content_sha256.
func ComputeIndexRevision(contentHash string, textTracks []asset.TextTrack) string {
	return sqcrypto.ComputeIndexRevision(contentHash, textTracks)
}

// ComputeContentHashWithTextTracks is the legacy alias for
// ComputeIndexRevision (canonical implementation lives in
// assets/crypto/clip_metadata_writer_hashes.go). Deprecated: use
// ComputeIndexRevision so byte identity is never conflated with the
// index revision.
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
