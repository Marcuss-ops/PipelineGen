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
//     and for any operator using collection snapshots.
//   - Qdrant-compatible — Qdrant point IDs must be either uint64 or
//     UUID strings; we emit canonical UUID v5 strings.
//   - Cryptographic collision resistance — SHA-1 over a project-private
//     16-byte namespace reduces birthday-bound collision probability
//     to ≈10^-12 at our corpus scale (~10^6 clips).
//
// Anti-regression gate: `rg -g '!*_test.go' "ID:\s*asset\.ID" internal/infrastructure/qdrant/`
// must return ZERO hits after this commit (and similarly for `r.ID`
// assignments to SearchResult.QdrantPointID outside the adapter).
//
// QDRANT-001 rebase-resolution (June 2026): the companion file
// ./canonical.go used to define a duplicate of this function (the
// identity `AssetIDPrefix + id` form). The duplicate was deleted
// during the rebase conflict resolution; this file is now the SOLE
// declaration of AssetIDToQdrantPointID across the qdrant package.
package schema

import "github.com/google/uuid"

// PipelineGenQdrantNamespace is the canonical UUID-v5 namespace for
// PipelineGen media assets. Hardcoded to permanently differentiate our
// hashes from the public namespaces exposed by github.com/google/uuid
// (the URL- and DNS-anchored variants), so any assetID string hashed
// under our namespace cannot collide with another project's UUIDv5
// derivation. We name these here descriptively rather than referencing
// the public symbols so the QDRANT-001 anti-regression gates
// cannot false-positive on the doc string.
//
// Changing this constant is a destructive migration: every existing
// Qdrant point would silently disappear from the index because the
// runtime alias would no longer match the previously-written IDs.
// Do not change.
var PipelineGenQdrantNamespace = uuid.MustParse("e5e9b4b1-2c8a-4f7d-9b3e-6c2d9a1f3e8b")

// AssetIDToQdrantPointID maps an internal AssetID — an opaque string,
// typically a YouTube-style token like "yt_<videoId>_<segStart>_<segEnd>"
// or an Artlist/voiceover-style id — to a deterministic UUID v5 string
// suitable for Qdrant point identity.
//
// Properties (see package doc):
//
//   - Deterministic: identical input ⇒ identical output, every call.
//   - Qdrant-compatible: result is a canonical UUID string.
//   - Collision-resistant: SHA-1 over the project namespace.
//
// Empty input yields the empty string so Point construction never
// silently substitutes a non-canonical ID. The caller (AssetToPoint)
// already validates non-emptiness of asset.ID; this symmetry lets the
// canonical boundary propagate the empty case cleanly.
func AssetIDToQdrantPointID(assetID string) string {
	if assetID == "" {
		return ""
	}
	return uuid.NewSHA1(PipelineGenQdrantNamespace, []byte(assetID)).String()
}
