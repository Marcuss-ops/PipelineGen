package overlays

import "testing"

// knownUnverifiedRendererSupported is deliberately EMPTY. Backing is now
// machine-checked against the real implementation sources: the emitted
// ChrononTemplate catalogs, the entity_visual_sequence camera recipes, the
// rendered map golden assets, the Chronon3d BASEMAP_STYLES palette, and the
// python map executor vocabulary. An empty list means every active entry that
// claims renderer support is backed by one of those sources. The test computes
// the true gap itself: if an entry is not backed anywhere, it fails here — and
// the fix is to back it (implement it) or set status=retired, NEVER to extend
// this list again.

// TestRegistryRendererSupportedIsBackedByCanonicalUniverse prevents the
// registry from claiming renderer support for ids the canonical catalog does
// not define.
func TestRegistryRendererSupportedIsBackedByCanonicalUniverse(t *testing.T) {
	universe := loadCanonicalMotionUniverse(t)
	backed := func(id string) bool {
		return universe.motionIDs[id] || universe.presentationIDs[id] || universe.decorIDs[id] ||
			universe.imagePresets[id] || universe.textPresets[id] ||
			universe.renderGoldenIDs[id] || universe.cameraRecipeIDs[id] ||
			universe.basemapStyleIDs[id] || universe.mapMotionIDs[id]
	}

	var gaps []string
	for _, entry := range canonicalAnimationRegistry.Entries() {
		if entry.Status != "active" || !entry.RendererSupported {
			continue
		}
		if !backed(entry.ID) {
			gaps = append(gaps, entry.ID)
		}
	}
	// Fail-closed on any residual gap: an id with renderer_supported=true but
	// no backing in any verified source must be implemented or retired, never
	// whitelisted again.
	for _, id := range gaps {
		t.Errorf("registry marks %q renderer_supported but no verified implementation source defines it (ChrononTemplate catalogs, entity_visual_sequence camera recipes, map goldens, Chronon3d BASEMAP_STYLES, map executor vocabulary): implement it upstream or set status=retired", id)
	}
}
