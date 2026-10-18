package overlays

// mapCompositionFamily groups certified Python map compositions by editorial
// intent. A family is eligible only when the item's pin count fits its range,
// so a composition is never chosen for data it cannot draw.
// The authoritative catalog is the animation registry (ChrononTemplate
// map_compositions.MAP_COMPOSITIONS projected via AnimationCandidates); this
// file holds no independent sampling pool — it only groups what the registry
// certifies, so adding a composition cannot diverge between planner and
// resolver.
type mapCompositionFamily struct {
	name    string
	members []string
	minPins int
	maxPins int
}

// mapCompositionFamilyOf mirrors the single catalog resolver classification
// (scripts/overlay MapCompositionFamilyOf): region_zoom is region_highlight,
// pulse/spotlight are location_focus, route/multi/comparison/editorial map to
// their families. Unknown ids stay in location_focus.
func mapCompositionFamilyOf(id string) string {
	switch id {
	case "region_zoom":
		return "region_highlight"
	case "pulse_marker", "spotlight":
		return "location_focus"
	case "route_line", "journey_route":
		return "journey_route"
	case "multi_locations", "multi_pin_reveal":
		return "multi_pin"
	case "comparison_split", "territory_compare":
		return "comparison"
	}
	if len(id) > 3 && id[:3] == "ed_" {
		return "editorial_documentary"
	}
	return "location_focus"
}

// mapCompositionFamilies builds families from the registry-certified random
// pool (the restrained sampler subset), grouped by the single family
// classification above. No hardcoded member lists.
func mapCompositionFamilies(pinCount int) []mapCompositionFamily {
	pool := AnimationRandomCandidates("map.composition")
	if len(pool) == 0 {
		pool = AnimationCandidates("map.composition")
	}
	byFamily := map[string][]string{}
	order := []string{}
	for _, id := range pool {
		// Pin-count eligibility comes from the registry (Min/MaxPinCount).
		eligible := false
		for _, e := range AnimationMapCompositionCandidates(pinCount) {
			if e == id {
				eligible = true
				break
			}
		}
		if !eligible {
			continue
		}
		fam := mapCompositionFamilyOf(id)
		if _, seen := byFamily[fam]; !seen {
			order = append(order, fam)
		}
		byFamily[fam] = append(byFamily[fam], id)
	}
	out := make([]mapCompositionFamily, 0, len(order))
	for _, fam := range order {
		out = append(out, mapCompositionFamily{name: fam, members: byFamily[fam], minPins: 1, maxPins: maxMapPinsPerGroup})
	}
	return out
}

// SelectMapComposition picks a certified composition for one static map item.
// The family is chosen among those compatible with pinCount, then the member
// within it. Both draws are seeded by (seed, scene, item), so the same job
// always yields the same composition and a new job may yield another. It
// returns "" when no family fits.
func SelectMapComposition(seed, sceneID, itemID string, pinCount int) string {
	if pinCount < 1 || pinCount > maxMapPinsPerGroup {
		return ""
	}
	eligible := mapCompositionFamilies(pinCount)
	if len(eligible) == 0 {
		return ""
	}
	scope := seed + "\x00" + sceneID
	family := eligible[SeededGroupIndex(scope, "map_family", itemID, len(eligible))]
	return family.members[SeededGroupIndex(scope, "map_composition", itemID, len(family.members))]
}
