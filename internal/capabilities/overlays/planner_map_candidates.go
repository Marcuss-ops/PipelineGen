package overlays

import (
	"strings"
)

func dedupeMapCandidatesByArticle(candidates []MapCandidate) []MapCandidate {
	out := make([]MapCandidate, 0, len(candidates))
	seen := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		key := mapCandidateArticleKey(candidate.Label)
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(candidate.Label))
		}
		if index, duplicate := seen[key]; duplicate {
			if candidate.DurationUS > out[index].DurationUS ||
				(candidate.DurationUS == out[index].DurationUS && candidate.Score > out[index].Score) {
				out[index] = candidate
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, candidate)
	}
	return out
}

// mapCandidateArticleKey normalizes only a leading Italian article so a
// "La Francia" label collides with "Francia". It does not touch the rest of
// the name, preserving official article-bearing names such as La Spezia.
func mapCandidateArticleKey(label string) string {
	key := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(label)), " "))
	for _, prefix := range []string{"l'", "l’"} {
		if strings.HasPrefix(key, prefix) {
			key = strings.TrimSpace(key[len(prefix):])
			break
		}
	}
	fields := strings.Fields(key)
	if len(fields) > 1 {
		switch fields[0] {
		case "la", "lo", "il", "le", "gli", "i", "un", "una", "uno":
			key = strings.Join(fields[1:], " ")
		}
	}
	return key
}

func preferAdministrativeMapCandidates(candidates []MapCandidate) []MapCandidate {
	if len(candidates) < 2 {
		return candidates
	}
	hasAdministrative := false
	for _, candidate := range candidates {
		if !isStreetLevelMapLabel(candidate.Label) {
			hasAdministrative = true
			break
		}
	}
	if !hasAdministrative {
		return candidates
	}
	filtered := make([]MapCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !isStreetLevelMapLabel(candidate.Label) {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func isStreetLevelMapLabel(label string) bool {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(label)))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "via", "viale", "piazza", "strada", "corso", "vicolo", "largo", "lungomare", "rotonda", "traversa":
		return true
	default:
		return false
	}
}

// mergeMapCandidateOccurrence spans every spoken occurrence of one place.
func mergeMapCandidateOccurrence(merged *MapCandidate, occurrence MapCandidate) {
	start := merged.StartUS
	end := merged.StartUS + merged.DurationUS
	if occurrence.StartUS < start {
		start = occurrence.StartUS
	}
	if occurrenceEnd := occurrence.StartUS + occurrence.DurationUS; occurrenceEnd > end {
		end = occurrenceEnd
	}
	merged.StartUS = start
	merged.DurationUS = end - start
	merged.StartMs = start / 1000
	merged.EndMs = (end + 999) / 1000
	if occurrence.Score > merged.Score {
		merged.Score = occurrence.Score
	}
}
