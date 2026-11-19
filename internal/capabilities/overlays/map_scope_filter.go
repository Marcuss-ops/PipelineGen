package overlays

import capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"

// ScopeFitsMapLevel reports whether a geocoder scope may be pinned on a map of
// the given cartographic level. An empty level accepts everything.
//
// The rule itself lives in the geocoding port: the provider applies it to every
// answer (a country-level lookup must not resolve a city) and the planner
// applies it to every candidate. Without one authority a municipality plate
// could keep "Israele" next to "Gerusalemme" because the two paths folded the
// provider's scope spellings differently.
func ScopeFitsMapLevel(scope, level string) bool {
	return capabilitygeocoding.ScopeFitsLevel(scope, level)
}

// FilterMapCandidatesByLevel keeps only the candidates whose grounded scope
// belongs to the requested cartographic level. This is a hard contract: when
// no candidate matches, the result is empty and the caller drops the map item.
// Substituting the unfiltered set would silently put a country on a city map
// (or every city on a country map), which is the exact mixing this filter
// exists to prevent.
func FilterMapCandidatesByLevel(candidates []MapCandidate, level string) []MapCandidate {
	if len(candidates) == 0 || !capabilitygeocoding.LevelConstrains(level) {
		return candidates
	}
	kept := make([]MapCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if ScopeFitsMapLevel(candidate.Scope, level) {
			kept = append(kept, candidate)
		}
	}
	return kept
}
