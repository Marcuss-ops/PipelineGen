package match

import (
	"sort"
	"strings"

	"velox/go-master/internal/service/association"
)

// SortTopMatches sorts matches by score and truncates to the limit.
func SortTopMatches(matches []association.ScoredMatch, limit int) []association.ScoredMatch {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			// Prioritize stock over artlist
			iIsStock := strings.Contains(matches[i].Source, "stock") && !strings.Contains(matches[i].Source, "artlist")
			jIsStock := strings.Contains(matches[j].Source, "stock") && !strings.Contains(matches[j].Source, "artlist")
			if iIsStock && !jIsStock {
				return true
			}
			if jIsStock && !iIsStock {
				return false
			}
			return matches[i].Title < matches[j].Title
		}
		return matches[i].Score > matches[j].Score
	})
	if limit > 0 && len(matches) > limit {
		return matches[:limit]
	}
	return matches
}
