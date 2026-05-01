package match

import (
	"sort"

	"velox/go-master/internal/service/association"
)

// SortTopMatches sorts matches by score and truncates to the limit.
func SortTopMatches(matches []association.ScoredMatch, limit int) []association.ScoredMatch {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Title < matches[j].Title
		}
		return matches[i].Score > matches[j].Score
	})
	if limit > 0 && len(matches) > limit {
		return matches[:limit]
	}
	return matches
}


