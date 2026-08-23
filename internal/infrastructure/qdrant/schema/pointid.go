// Package qdrant — canonical AssetID → QdrantPointID boundary.
//
// QDRANT-001 (June 2026): the audit verdict required ONE canonical
// function — AssetIDToQdrantPointID — to replace the ad-hoc point
// assignments previously scattered across this package (some sites
// used `ID: asset.ID` directly, others `QdrantPointID: r.ID`). The
// guarantees are:
//
//   - Deterministic — re-indexing the same asset yields the same point.
//     Critical for incremental diffs in the reconciler (QDRANT-005)
//     and for any operator using collection snapshots. Every outbox
//     replay hashes the same assetID to the same point, so the upsert
//     updates the existing point rather than creating a duplicate.
//   - Qdrant-compatible — Qdrant point IDs must be either uint64 or
//     UUID strings; we emit canonical UUID v8 strings (Fase 12 / Commit 1).
//   - Cryptographic collision resistance — SHA-256 over the bare
//     assetID reduces birthday-bound collision probability to
//     ≈10^-38 at our corpus scale (~10^6 clips), well below the
//     ≈10^-12 that UUID v5/SHA-1 provided.
//
// FASE 12 (July 2026) — deterministic point ID via SHA-256:
// the user-spec literal "sha256 di asset ID" required switching the
// hash from SHA-1 (used pre-Fase-12 via uuid.NewSHA1) to SHA-256.
// The output UUID format promoted from v5 to v8 (custom) so the wire
// shape encodes the algorithm change (any operator inspecting the
// Qdrant collection can distinguish v8-shalow from v5-sha1 points).
// Operationally the change requires a full reindex (existing v5
// points become orphaned in the v8 universe) but replay-safety is
// PRESERVED: from this commit forward, every replay of the same
// assetID computes the same SHA-256 → v8 point, irrespective of
// how many times the outbox dispatches the same event.
//
// Anti-regression gate: `rg -g '!*_test.go' "ID:\\s*asset\\.ID" internal/infrastructure/qdrant/`
// must return ZERO hits after this commit (and similarly for `r.ID`
// assignments to SearchResult.QdrantPointID outside the adapter).
//
// QDRANT-001 rebase-resolution (June 2026): the companion file
// ./canonical.go used to define a duplicate of this function (the
// identity `AssetIDPrefix + id` form). The duplicate was deleted
// during the rebase conflict resolution; this file is now the SOLE
// declaration of AssetIDToQdrantPointID across the qdrant package.
package schema

import (
	"encoding/hex"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

	"github.com/google/uuid"
)

// PipelineGenQdrantNamespace remains exported for backward compat
// (verification/schema_aliases.go re-exports it). PRE-FASE-12 it was
// the canonical UUID-v5 namespace fed to uuid.NewSHA1 together with
// the assetID bytes. POST-FASE-12 (this commit) the canonical
// derivation no longer consumes the namespace (SHA-256 of the bare
// assetID is sufficient for the v8 output), but the var is kept so
// existing consumers that reference the symbol continue to compile.
// Changing the bytes of this constant is a no-op for the canonical
// point ID derivation; it is retained solely as a back-compat
// surface. Do not consume it in new code.
var PipelineGenQdrantNamespace = uuid.MustParse("e5e9b4b1-2c8a-4f7d-9b3e-6c2d9a1f3e8b")

// AssetIDToQdrantPointID maps an internal AssetID — an opaque string,
// typically a YouTube-style token like "yt_<videoId>_<segStart>_<segEnd>"
// or an Artlist/voiceover-style id — to a deterministic point ID
// suitable for Qdrant.
//
// Properties (see package doc):
//
//   - Deterministic: identical input ⇒ identical output, every call.
//     The replay-safety contract (Fase 12): every outbox replay of
//     the same assetID produces the same point, so upsert updates
//     the existing row instead of inserting a duplicate.
//   - Qdrant-compatible: result is a canonical UUID string.
//     Specifically, UUID v8 ("custom") per RFC 9562 §5.8 — first
//     16 bytes of the sha256 digest, with the version nibble set
//     to 8 (custom) and the variant bits set to RFC 4122.
//   - Collision-resistant: SHA-256 of the bare assetID; no namespace
//     pre-mixing. (Pre-Fase-12 this was SHA-1 over a project
//     namespace; the SHA-256 swap is per the user-spec literal.)
//
// Empty input yields the empty string so Point construction never
// silently substitutes a non-canonical ID. The caller (AssetToPoint)
// already validates non-emptiness of asset.ID; this symmetry lets the
// canonical boundary propagate the empty case cleanly.
func AssetIDToQdrantPointID(assetID string) string {
	if assetID == "" {
		return ""
	}
	hash := digest.SHA256Bytes([]byte(assetID))
	raw, err := hex.DecodeString(hash)
	if err != nil {
		return ""
	}
	var b [16]byte
	copy(b[:], raw[:16])
	// UUID v8 per RFC 9562 §5.8: set the high nibble of byte 6 to
	// 0x8 (custom version). The v5/v8 distinction is opaque to
	// Qdrant (both are canonical UUID strings), but a future
	// operator inspecting the Qdrant collection can distinguish
	// pre-Fase-12 v5/SHA-1 points from post-Fase-12 v8/SHA-256 points
	// at the UUID-nibble level — useful for rollout forensics.
	b[6] = (b[6] & 0x0f) | 0x80
	// RFC 4122 variant bits (byte 8 high bits = 0b10).
	b[8] = (b[8] & 0x3f) | 0x80
	return uuid.UUID(b).String()
}
