package overlays

import (
	"sort"
	"testing"
)

// knownUnverifiedRendererSupported lists active registry entries that carry
// renderer_supported=true but have no backing id in the canonical
// ChrononTemplate catalog (motions, presentations, decor). The flag is
// hand-written, so these are debt, not proof of rendering. The test below
// requires this list to match the real gap exactly: new unbacked entries fail,
// and entries must be removed here once they are verified or retired.
var knownUnverifiedRendererSupported = map[string]bool{
	"badge_side": true, "bare_big": true, "bracket_label": true,
	"camera_motion_photo_reveal_right": true, "caption_card": true, "card_glass": true,
	"cinematic_fade": true, "clean_fade": true, "diffuse_city_beacon": true, "dot_ring": true,
	"ed_hormuz_routes": true, "ed_iran_glow": true, "editorial_serif": true, "ed_mocha_tactical": true,
	"ed_saudi_base": true, "ed_yemen_fill": true, "ed_yemen_markers": true, "esri_dark": true,
	"esri_light": true, "esri_ocean": true, "fast_push": true, "focus_title": true,
	"gentle_fade": true, "global_context": true, "gpin_bubble": true, "huge_center": true,
	"icon_label": true, "info_card": true, "label_reveal": true, "location_focus": true,
	"location_pulse": true, "multi_locations": true, "orbit_arrival": true, "outline_box": true,
	"photo_callout": true, "pop_in": true, "pulse_marker": true, "quick_fade": true,
	"quiet_bloom": true, "region_fill": true, "region_zoom": true, "ribbon": true,
	"rise_up": true, "route_line": true, "signature_dive": true, "silky_fade": true,
	"simple_label": true, "slow_approach": true, "slow_fade": true, "small_above": true,
	"soft_glow": true, "soft_tracking": true, "spotlight": true, "steady_hold": true,
	"subtle_halo": true, "tilt_reveal": true, "title_top": true, "vertical_callout": true,
	"wide_context": true, "word_soft_fade": true,
}

// TestRegistryRendererSupportedIsBackedByCanonicalUniverse prevents the
// registry from claiming renderer support for ids the canonical catalog does
// not define.
func TestRegistryRendererSupportedIsBackedByCanonicalUniverse(t *testing.T) {
	universe := loadCanonicalMotionUniverse(t)
	backed := func(id string) bool {
		return universe.motionIDs[id] || universe.presentationIDs[id] || universe.decorIDs[id] ||
			universe.imagePresets[id] || universe.textPresets[id]
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
	sort.Strings(gaps)

	gapSet := make(map[string]bool, len(gaps))
	for _, id := range gaps {
		gapSet[id] = true
		if !knownUnverifiedRendererSupported[id] {
			t.Errorf("registry marks %q renderer_supported but the canonical ChrononTemplate universe does not define it", id)
		}
	}
	for id := range knownUnverifiedRendererSupported {
		if !gapSet[id] {
			t.Errorf("knownUnverifiedRendererSupported lists %q, but it is now backed or no longer active: remove it from the debt list", id)
		}
	}
}
