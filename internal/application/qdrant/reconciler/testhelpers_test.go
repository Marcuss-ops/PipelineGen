// Package reconciler test helpers — sealed across the reconciler test
// surface. Keeping these in a dedicated file (testhelpers_test.go)
// isolates "shared fixture plumbing" from the per-test files so a
// future PR that refactors scanner_test.go / service_test.go will
// not accidentally relocate the seal.
package reconciler

// canonicalPointID is the canonical AssetIDToQdrantPointID mapping
// (= assetID with "pt-" prefix) used by every reconciler test
// fixture. Mirrors production qdrant.AssetIDToQdrantPointID.
//
// Why this is a sealed helper:
//
//   - The scanner's NonCanonicalPointID classifier compares observed
//     point-IDs against canonicalPointID("a1") == "pt-a1". Tests that
//     substitute a different prefix (e.g. "asset-a1") silently
//     diverge from production semantics, and the divergence is only
//     caught by manual review of the fixture.
//
//   - The hyphen-vs-underscore trap ("pt-a1" vs "pt_a1") used to live
//     as inline `func(s string) string { return "pt-" + s }` literals
//     at every call site; this helper locks the canonical shape in
//     exactly one place.
//
// Use this helper instead of inline `func(s string) string { ... }`
// literals; assign it to AssetPointIDFunc in fixtures with
// `withPointIDFor(canonicalPointID)` so all tests exercise the same
// prefix derivation.
func canonicalPointID(assetID string) string {
	return "pt-" + assetID
}
