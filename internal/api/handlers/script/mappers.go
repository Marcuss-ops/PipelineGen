package script

import (
	"velox/go-master/internal/service/association"
	"velox/go-master/pkg/models"
)

func modelClipsToScoredMatches(clips []models.MediaAsset, details string, source string, link string) []association.ScoredMatch {
	matches := make([]association.ScoredMatch, 0, len(clips))
	for _, c := range clips {
		l := link
		if l == "" {
			l = c.ExternalURL
		}
		matches = append(matches, association.ScoredMatch{
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
