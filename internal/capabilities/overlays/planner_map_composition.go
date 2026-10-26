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

// PortedMapCompositions lists the composition ids the single dynamic map
// renderer draws with certified parity gates. The Python renderer is retired
// (TICKET-MAPS-NATIVE-PORT fase 7): the native renderer
// (ChrononPlugin/maps_dynamic_map_render) serves the BASE flyOVER
// presentation only, and every composed family (region, multi-location,
// journey-route, comparison, editorial scenes) is demolition debt awaiting
// its own bit-exact parity gate BEFORE it may be served. The seeded draw
// therefore selects nothing today and the map presents the certified base
// flyover — incompatible variants are excluded from random selection, never
// silently swapped for a family the executing renderer cannot draw. A family
// joins this list the moment it lands its parity gate and becomes servable.
//
// This is the SINGLE ported-subset declaration: the resolver's random pool
// (scripts/overlay MapCompositionRandomPool) projects this same list.
func PortedMapCompositions() []string {
	return nil
}

// mapCompositionFamilies builds families from the registry-certified random
// pool (the restrained sampler subset) restricted to the compositions the
// single dynamic renderer actually draws (PortedMapCompositions), grouped by
// the single family classification above. No hardcoded member lists: the
// registry certifies the catalog, the ported subset decides servability.
func mapCompositionFamilies(pinCount int) []mapCompositionFamily {
	ported := map[string]bool{}
	for _, id := range PortedMapCompositions() {
		ported[id] = true
	}
	pool := []string{}
	for _, id := range AnimationRandomCandidates("map.composition") {
		if ported[id] {
			pool = append(pool, id)
		}
	}
	if len(pool) == 0 {
		for _, id := range AnimationCandidates("map.composition") {
			if ported[id] {
				pool = append(pool, id)
			}
		}
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

// SelectMapComposition picks a certified, servable composition for one static
// map item. The family is chosen among those compatible with pinCount and
// ported to the single dynamic renderer, then the member within it. Both draws
// are seeded by (seed, scene, item), so the same job always yields the same
// composition and a new job may yield another. It returns "" when no family
// fits — today always "" (the base flyover presentation), because no composed
// family has landed its parity gate yet.
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
