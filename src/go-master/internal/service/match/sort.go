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

// SelectBestMatchLink picks the link from the highest scoring match.
func SelectBestMatchLink(matches []association.ScoredMatch) string {
	if len(matches) == 0 {
		return ""
	}

	bestScore := -1
	bestLink := ""

	for _, m := range matches {
		if m.Score > bestScore && m.Link != "" {
			bestScore = m.Score
			bestLink = m.Link
		}
	}
	return bestLink
}
