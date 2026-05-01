package script

import "velox/go-master/pkg/models"

func modelClipsToScoredMatches(clips []models.Clip, details string, source string, link string) []scoredMatch {
	matches := make([]scoredMatch, 0, len(clips))
	for _, c := range clips {
		l := link
		if l == "" {
			l = c.ExternalURL
		}
		matches = append(matches, scoredMatch{
			Title:   c.Name,
			Path:    c.LocalPath,
			Score:   100,
			Source:  source,
			Link:    l,
			Details: details,
		})
	}
	return matches
}
