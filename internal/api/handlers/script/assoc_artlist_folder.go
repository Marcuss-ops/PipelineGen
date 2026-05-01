package script

import (
	"context"
	"strings"

	"velox/go-master/internal/matching"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/textutil"
)

// ArtlistFolderAssociation matches a segment against known Artlist folders.
type ArtlistFolderAssociation struct {
	repo           *clips.Repository
	nodeScraperDir string
	topic          string
}

func NewArtlistFolderAssociation(repo *clips.Repository, nodeScraperDir, topic string) *ArtlistFolderAssociation {
	return &ArtlistFolderAssociation{repo: repo, nodeScraperDir: nodeScraperDir, topic: topic}
}

func (a *ArtlistFolderAssociation) Associate(ctx context.Context, segment *TimelineSegment) ([]scoredMatch, error) {
	if a.repo == nil && strings.TrimSpace(a.nodeScraperDir) == "" {
		return nil, nil
	}

	searchTerm := segmentAssociationSubject(segment)
	if searchTerm == "" {
		keywords := segmentAssociationKeywords(segment)
		if len(keywords) > 0 {
			searchTerm = keywords[0]
		} else {
			return nil, nil
		}
	}

	candidates, err := buildTimelineArtlistFolderCandidates(ctx, a.repo, a.nodeScraperDir)
	if err != nil || len(candidates) == 0 {
		return nil, err
	}

	queryTokens := textutil.Tokenize(searchTerm)
	slug := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(searchTerm)), " ", "-")

	matches := make([]scoredMatch, 0, len(candidates))
	for _, candidate := range candidates {
		targetTokens := textutil.Tokenize(candidate.Name + " " + candidate.Path)
		score := matching.CalculateTokenScore(queryTokens, targetTokens)

		candidateSlug := strings.ReplaceAll(strings.ToLower(candidate.Name), " ", "-")
		if strings.Contains(candidateSlug, slug) || strings.Contains(slug, candidateSlug) {
			score += 20
		}
		score += preferredCandidateBoost(segment, candidate.Path, candidate.Link, candidate.Name)

		if score > 40 {
			matches = append(matches, scoredMatch{
				Title:   candidate.Name,
				Path:    candidate.Path,
				Score:   score,
				Source:  string(timelineAssetSourceArtlistFolder),
				Link:    candidate.Link,
				Details: "Artlist folder directly matches segment subject",
			})
		}
	}

	return matches, nil
}
