package association

import (
	"context"

	"velox/go-master/internal/matching"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/textutil"
)

// ClipDriveAssociation cerca clip specifiche nel database delle clip scaricate.
type ClipDriveAssociation struct {
	repo *clips.Repository
}

func NewClipDriveAssociation(repo *clips.Repository) *ClipDriveAssociation {
	return &ClipDriveAssociation{repo: repo}
}

func (a *ClipDriveAssociation) Associate(ctx context.Context, input SegmentInput) ([]ScoredMatch, error) {
	if a.repo == nil {
		return nil, nil
	}

	// Usiamo sia il subject che le keywords per una ricerca più ampia
	keywords := textutil.Tokenize(input.Subject)
	keywords = append(keywords, input.Keywords...)

	if len(keywords) == 0 {
		return nil, nil
	}

	searchTerm := input.Subject
	if searchTerm == "" && len(input.Keywords) > 0 {
		searchTerm = input.Keywords[0]
	}

	clipsList, err := a.repo.SearchClips(ctx, searchTerm)
	if err != nil {
		clipsList, _ = a.repo.SearchClips(ctx, input.Subject)
	}

	queryTokens := textutil.Tokenize(input.Subject)
	var matches []ScoredMatch
	for _, c := range clipsList {
		targetTokens := textutil.Tokenize(c.Name)
		score := matching.CalculateTokenScore(queryTokens, targetTokens)

		if score > 30 {
			matches = append(matches, ScoredMatch{
				Title:  c.Name,
				Path:   c.LocalPath,
				Score:  score,
				Source: "clip_drive",
				Link:   c.ExternalURL,
			})
		}
	}

	return matches, nil
}
