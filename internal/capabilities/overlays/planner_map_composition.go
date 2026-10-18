package overlays

// mapCompositionFamily groups certified Python map compositions by editorial
// intent. A family is eligible only when the item's pin count fits its range,
// so a composition is never chosen for data it cannot draw.
type mapCompositionFamily struct {
	name    string
	members []string
	minPins int
	maxPins int
}

// mapCompositionFamilies lists the certified compositions (ChrononTemplate
// map_compositions.MAP_COMPOSITIONS) by family.
var mapCompositionFamilies = []mapCompositionFamily{
	{name: "location_focus", members: []string{"region_zoom", "spotlight"}, minPins: 1, maxPins: maxMapPinsPerGroup},
	{name: "multi_pin", members: []string{"pulse_marker", "spotlight"}, minPins: 2, maxPins: maxMapPinsPerGroup},
}

// SelectMapComposition picks a certified composition for one static map item.
// The family is chosen among those compatible with pinCount, then the member
// within it. Both draws are seeded by (seed, scene, item), so the same job
// always yields the same composition and a new job may yield another. It
// returns "" when no family fits.
func SelectMapComposition(seed, sceneID, itemID string, pinCount int) string {
	if pinCount < 1 {
		return ""
	}
	eligible := make([]mapCompositionFamily, 0, len(mapCompositionFamilies))
	for _, family := range mapCompositionFamilies {
		if pinCount >= family.minPins && pinCount <= family.maxPins {
			eligible = append(eligible, family)
		}
	}
	if len(eligible) == 0 {
		return ""
	}
	scope := seed + "\x00" + sceneID
	family := eligible[SeededGroupIndex(scope, "map_family", itemID, len(eligible))]
	return family.members[SeededGroupIndex(scope, "map_composition", itemID, len(family.members))]
}
