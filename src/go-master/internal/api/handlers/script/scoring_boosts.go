package script

import "strings"

func preferredCandidateBoost(segment *TimelineSegment, candidatePath, candidateLink, candidateTitle string) int {
	if segment == nil {
		return 0
	}

	preferred := append([]string{}, segment.PreferredStockPaths...)
	if len(preferred) == 0 {
		return 0
	}

	candidatePath = strings.ToLower(strings.TrimSpace(candidatePath))
	candidateLink = strings.ToLower(strings.TrimSpace(candidateLink))
	candidateTitle = strings.ToLower(strings.TrimSpace(candidateTitle))

	for _, pref := range preferred {
		pref = strings.ToLower(strings.TrimSpace(pref))
		if pref == "" {
			continue
		}
		if candidatePath != "" && strings.Contains(candidatePath, pref) {
			return 35
		}
		if candidateLink != "" && strings.Contains(candidateLink, pref) {
			return 35
		}
		if candidateTitle != "" && strings.Contains(candidateTitle, pref) {
			return 20
		}
	}

	return 0
}
