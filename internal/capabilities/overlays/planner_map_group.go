package overlays

import "sort"

// planner_map_group.go owns the map-group presentation strategy and the
// narrative-proximity grouping helpers extracted from planner_map.go to keep
// that file inside the godlike/08 max_lines_per_file_strict cap (600). The
// split is a pure move: the functions are the ones mapItemsForScene called
// before, unchanged.

// mapTourMinimumSeconds mirrors the dynamic renderer's authored multi-stop
// route budget: 3s opening beat plus 4.5s per travelled leg. The planner uses
// it BEFORE rendering to decide whether a stop-by-stop tour fits the spoken
// span, so a short beat gets a declared multi-pin composition instead of a
// render-time failure or a silently reduced tour.
func mapTourMinimumSeconds(pinCount int) float64 {
	if pinCount <= 1 {
		return 3.0
	}
	return 3.0 + 4.5*float64(pinCount-1)
}

// mapGroupTourStrategy is the planner's editorial decision for one map group:
// "tour" when the spoken duration can carry a stop-by-stop camera route, and
// "multi_pin" — a declared alternative composition that keeps every place on
// screen — when it cannot. The renderer never changes this content decision.
func mapGroupTourStrategy(pinCount int, durationUS int64) string {
	if pinCount <= 1 {
		return "tour"
	}
	if float64(durationUS)/1_000_000 >= mapTourMinimumSeconds(pinCount) {
		return "tour"
	}
	return "multi_pin"
}

// maxMapNarrativeGapUS is the largest silence between two spoken places that
// still keeps them in the same narrative group. Places named in quick
// succession belong to ONE composition (a MapGroup), exactly like nearby
// images fuse into one image group.
const maxMapNarrativeGapUS = 8_000_000

// groupMapPlacesByNarrativeProximity groups places by narrative proximity
// first — consecutive places in spoken order whose gap stays within
// maxMapNarrativeGapUS form one group — then splits any group above size into
// consecutive chunks. Every place lands in exactly one chunk; nothing is ever
// dropped. The result is in spoken order.
func groupMapPlacesByNarrativeProximity(places []MapCandidate, size int) [][]MapCandidate {
	if len(places) == 0 || size <= 0 {
		return nil
	}
	ordered := sortedMapPlacesSpokenOrder(places)
	clusters := make([][]MapCandidate, 0, len(ordered))
	current := []MapCandidate{ordered[0]}
	for _, place := range ordered[1:] {
		previousEnd := current[len(current)-1].StartUS + current[len(current)-1].DurationUS
		if place.StartUS-previousEnd > maxMapNarrativeGapUS {
			clusters = append(clusters, current)
			current = nil
		}
		current = append(current, place)
	}
	clusters = append(clusters, current)
	groups := make([][]MapCandidate, 0, len(clusters))
	for _, cluster := range clusters {
		groups = append(groups, chunkMapPlaces(cluster, size)...)
	}
	return groups
}

// sortedMapPlacesSpokenOrder is the ONE spoken-order sort shared by grouping,
// pin projection and route endpoints: entity id breaks ties so the order is
// total and deterministic.
func sortedMapPlacesSpokenOrder(places []MapCandidate) []MapCandidate {
	ordered := append([]MapCandidate(nil), places...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].StartUS != ordered[j].StartUS {
			return ordered[i].StartUS < ordered[j].StartUS
		}
		return ordered[i].EntityID < ordered[j].EntityID
	})
	return ordered
}

// chunkMapPlaces splits places, in spoken order, into consecutive chunks of at
// most size entries. Every place lands in exactly one chunk.
func chunkMapPlaces(places []MapCandidate, size int) [][]MapCandidate {
	if len(places) == 0 || size <= 0 {
		return nil
	}
	ordered := sortedMapPlacesSpokenOrder(places)
	chunks := make([][]MapCandidate, 0, (len(ordered)+size-1)/size)
	for start := 0; start < len(ordered); start += size {
		end := start + size
		if end > len(ordered) {
			end = len(ordered)
		}
		chunks = append(chunks, ordered[start:end])
	}
	return chunks
}
