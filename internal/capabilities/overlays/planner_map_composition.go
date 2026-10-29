package overlays

import "strings"

// Map composition families: the editorial look one composition draws. The
// random draw is family-first, so one job varies across looks instead of
// deepening one of them. These constants are the SINGLE spelling of the family
// names: the resolver facade (scripts/overlay MapCompositionFamily) aliases
// them and the classification below is the only switch.
const (
	MapFamilyLocationFocus = "location_focus"
	MapFamilyMultiPin      = "multi_pin"
	MapFamilyJourney       = "journey_route"
	MapFamilyRegion        = "region_highlight"
	MapFamilyComparison    = "comparison"
	MapFamilyEditorial     = "editorial_documentary"
)

// mapCompositionFamily groups certified map compositions by editorial intent.
// A family is eligible only when the item's pin count fits its range, so a
// composition is never chosen for data it cannot draw.
// The authoritative catalog is the animation registry, and its map.composition
// entries are the projection of the native renderer's own vocabulary
// (ChrononPlugin/plugins/maps/maps-renderer-catalog.v1.json); this file holds
// no independent sampling pool — it only groups what the registry certifies,
// so adding a composition cannot diverge between planner and resolver.
type mapCompositionFamily struct {
	name    string
	members []string
	minPins int
	maxPins int
}

// MapCompositionFamilyOf classifies one certified composition id by the look
// it draws (the family is not a registry field: the registry owns identity and
// servability, this switch owns editorial grouping). The resolver facade
// (scripts/overlay MapCompositionFamilyOf) wraps it, so planner and resolver
// can never disagree about a family. Unknown ids stay in location_focus.
func MapCompositionFamilyOf(id string) string {
	switch strings.TrimSpace(id) {
	case "territory_glow_reveal", "area_glow_pulse":
		return MapFamilyRegion
	case "route_line", "multi_leg_flight":
		return MapFamilyJourney
	}
	return MapFamilyLocationFocus
}

// PortedMapCompositions lists the map compositions the single dynamic map
// renderer (ChrononPlugin/maps_dynamic_map_render) exposes. The catalog is
// intentionally limited to production map treatments; visual concept
// showcases remain in the plugin archive and cannot be selected by the planner.
//
// This is the SINGLE ported-subset declaration: the resolver's random pool
// (scripts/overlay MapCompositionRandomPool) and the backend capability
// resolution project this same list.
func PortedMapCompositions() []string {
	return []string{
		"route_line",
		"multi_leg_flight",
		"territory_glow_reveal",
		"area_glow_pulse",
	}
}

// mapCompositionFamilies builds families from the registry-certified random
// pool (the restrained sampler subset) restricted to the compositions the
// single dynamic renderer actually draws (PortedMapCompositions), grouped by
// the single family classification above. No hardcoded member lists: the
// registry certifies the catalog (a projection of the ChrononPlugin map
// catalog), the ported subset decides servability.
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
		fam := MapCompositionFamilyOf(id)
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
// fits the data — the certified base flyover presentation, never an unportable
// composition.
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
