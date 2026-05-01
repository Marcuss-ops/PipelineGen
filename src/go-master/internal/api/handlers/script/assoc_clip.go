package script

import (
	"context"

	"velox/go-master/internal/matching"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/sliceutil"
	"velox/go-master/pkg/textutil"
)

// ClipDriveAssociation cerca clip specifiche nel database delle clip scaricate
type ClipDriveAssociation struct {
	repo *clips.Repository
}

func NewClipDriveAssociation(repo *clips.Repository) *ClipDriveAssociation {
	return &ClipDriveAssociation{repo: repo}
}

func (a *ClipDriveAssociation) Associate(ctx context.Context, segment *TimelineSegment) ([]scoredMatch, error) {
	if a.repo == nil {
		return nil, nil
	}

	// Usiamo sia il subject che le keywords per una ricerca più ampia
	keywords := sliceutil.UniqueStrings(append(textutil.Tokenize(segmentAssociationSubject(segment)), segmentAssociationKeywords(segment)...))
	if len(keywords) == 0 {
		return nil, nil
	}

	// Cerchiamo clip che corrispondono a queste keyword nel DB dedicato alle clip.
	clipsList, err := a.repo.SearchClipsByKeywords(ctx, keywords, 10)
	if err != nil {
		clipsList, _ = a.repo.SearchClips(ctx, segment.Subject)
	}

	queryTokens := textutil.Tokenize(segmentAssociationSubject(segment))
	var matches []scoredMatch
	for _, c := range clipsList {
		targetTokens := textutil.Tokenize(c.Name)
		score := matching.CalculateTokenScore(queryTokens, targetTokens)
		score += preferredCandidateBoost(segment, c.FolderPath, c.ExternalURL, c.Name)

		if score > 30 {
			matches = append(matches, scoredMatch{
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
