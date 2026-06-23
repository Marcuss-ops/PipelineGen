// Test ensuring Drive folder resolution honors StoreOptions.AssetTree + TreeSources (FUTURE).
//
// STATUS (June 2026, wave-1 of refactor/composition for problem #8):
// This test currently SKIPS. StoreOptions.AssetTree + TreeSources are
// stored on Store via NewStoreWithOptions, but the existing EnsureDriveFolder
// implementation routes on MediaType alone — treeService and treeSourceMap
// are not consulted in production code paths yet.
//
// The legacy SetAssetTree + SetTreeSource (removed in wave-1) were ALREADY
// writing into a black hole before the migration, so this is not a regression
// introduced by the refactor; it is a dormant integration point. Per
// git archaeology the field was never read in EnsureDriveFolder even before
// the refactor.
//
// When the team implements the style-aware fallback path in EnsureDriveFolder
// (after the MediaType switch, s.treeService.StyleFolder(req) → s.treeSourceMap
// cache look), this test should be promoted to a real assertion. Until then
// it documents the asymmetry so future waves 2-5 can cite this test as the
// migration signal.
package drive

import "testing"

// TestStoreOptions_AssetTreeAndTreeSources_NotYetConsumed documents the
// dormant state. Replace t.Skip with concrete assertions once EnsureDriveFolder
// is wired to consult treeService + treeSourceMap — at that point the test
// becomes the regression guard for the style-aware routing.
func TestStoreOptions_AssetTreeAndTreeSources_NotYetConsumed(t *testing.T) {
	t.Skip("TODO wave-N: StoreOptions.AssetTree + TreeSources stored but EnsureDriveFolder does not consult them yet. Wire into the MediaType switch fallback path; see commit body of refactor(composition) wave-1 (Commit 092de5f) for migration path.")
}
