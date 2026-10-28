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
	case "thermal_heatmap_overlay", "isochrone_travel_bubble", "topographic_contour_rise",
		"flood_sea_level_inundation", "wildfire_burn_scar_expansion",
		"naval_chokepoint_exclusion", "frontline_fluid_corridor", "territory_polygon_wipe":
		return MapFamilyRegion
	case "cell_tower_triangulation", "port_hub_radial_burst":
		return MapFamilyMultiPin
	case "route_line", "multi_leg_flight", "timeline_suspect_trail",
		"great_circle_polar_route", "pipeline_energy_flow", "supply_chain_artery_pulse",
		"subsea_cable_glow_network", "missile_arc_ballistic":
		return MapFamilyJourney
	case "satellite_before_after_split", "multi_camera_split_matrix":
		return MapFamilyComparison
	case "satellite_dive_lock", "night_vision_infrared", "air_defense_interception_dome",
		"cinematic_anchor_pitch_tilt", "crime_scene_radar":
		return MapFamilyLocationFocus
	}
	return MapFamilyLocationFocus
}

// PortedMapCompositions lists the map compositions the single dynamic map
// renderer (ChrononPlugin/maps_dynamic_map_render) draws from grounded places
// alone. The native documentary gallery IS the executing renderer's own
// vocabulary (ChrononPlugin/plugins/maps/maps-renderer-catalog.v1.json, the
// projection registered as map.composition), so every composition it draws
// without extra authored data is servable. Two compositions stay out because
// they need payload fields the planner never authors (crime_scene_radar needs
// crime_scene_radius_m, territory_polygon_wipe needs territory_feature), and
// every composed scene/tour family of the retired Python renderer is gone.
// A composition joins this list the moment the renderer draws it from the data
// the planner supplies.
//
// This is the SINGLE ported-subset declaration: the resolver's random pool
// (scripts/overlay MapCompositionRandomPool) and the backend capability
// resolution project this same list.
func PortedMapCompositions() []string {
	return []string{
		"route_line",
		"satellite_dive_lock",
		"multi_leg_flight",
		"cell_tower_triangulation",
		"isochrone_travel_bubble",
		"timeline_suspect_trail",
		"thermal_heatmap_overlay",
		"night_vision_infrared",
		"frontline_fluid_corridor",
		"naval_chokepoint_exclusion",
		"missile_arc_ballistic",
		"multi_camera_split_matrix",
		"air_defense_interception_dome",
		"subsea_cable_glow_network",
		"supply_chain_artery_pulse",
		"great_circle_polar_route",
		"port_hub_radial_burst",
		"pipeline_energy_flow",
		"satellite_before_after_split",
		"topographic_contour_rise",
		"flood_sea_level_inundation",
		"wildfire_burn_scar_expansion",
		"cinematic_anchor_pitch_tilt",
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
