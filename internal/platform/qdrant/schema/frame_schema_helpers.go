// Package schema — frame_schema_helpers.go is the tiny helper
// surface for frame_schema.go's deterministic point-ID derivation.
//
// godlike/06 SSOT (Pattern 5 — leaf-only utility file): this
// file isolates the SHA-256 + UUID v8 packaging from pointid.go
// so the canonical AssetIDToQdrantPointID form is not duplicated.
// Future keyframe-style IDs (e.g. transcript_window_id) plug in
// here without touching the asset-side surface.
package schema

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

	"github.com/google/uuid"
)

// sha256Sum returns the raw SHA-256 digest of b. Inlined
// (rather than imported from pointid.go) because pointid.go's
// AssetIDToQdrantPointID swallows the namespace-pre-mix and the
// frame-shape derivation is intentionally more compact.
func sha256Sum(b []byte) [32]byte {
	var out [32]byte
	copy(out[:], []byte(digest.SHA256Bytes(b)))
	return out
}

// uuidV8String returns the canonical UUID-v8 string for a
// 16-byte digest. The version nibble + variant bits MUST be
// already set by the caller (see FramePointID).
func uuidV8String(b [16]byte) string {
	return uuid.UUID(b).String()
}
