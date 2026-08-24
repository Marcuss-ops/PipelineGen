// Package reconciler test helpers — sealed across the reconciler test
// surface. Keeping these in a dedicated file (testhelpers_test.go)
// isolates "shared fixture plumbing" from the per-test files so a
// future PR that refactors scanner_test.go / service_test.go will
// not accidentally relocate the seal.
package reconciler

// canonicalPointID is a TEST-ONLY stub that simplifies the real
// schema.AssetIDToQdrantPointID (UUID v5 with SHA-1) to a predictable
// "pt-<assetID>" prefix. This stub is INTENTIONALLY simpler than
// production — the reconciler's NonCanonicalPointID classifier only
// needs to detect that a point ID does NOT match the canonical
// derivation; "pt-a1" vs "wrong-uuid" is sufficient for testing.
//
// Task 3 (July 2026): explicitly documented as TEST-ONLY. The ONLY
// production declaration of AssetIDToQdrantPointID lives in
// internal/platform/qdrant/schema/pointid.go (UUID v5 SHA-1).
// This stub MUST NOT be used in production code — the composition_test
// gate "TestComposition_AssetIDToQdrantPointID_SingleDeclaration"
// enforces exactly 1 production declaration.
//
// Why this is a sealed helper:
//   - Prevents inline point-ID derivation at test call sites
//   - Locks "pt-" prefix shape in a single place
//   - Makes future migration to the real function a one-line change
func canonicalPointID(assetID string) string {
	return "pt-" + assetID
}
