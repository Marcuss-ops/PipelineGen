package association

import (
	"context"
)

// ArtlistFolderAssociation matches a segment against known Artlist folders.
type ArtlistFolderAssociation struct {
	s *Service
}

func NewArtlistFolderAssociation(s *Service) *ArtlistFolderAssociation {
	return &ArtlistFolderAssociation{s: s}
}

func (a *ArtlistFolderAssociation) Associate(ctx context.Context, input SegmentInput) ([]ScoredMatch, error) {
	if a.s == nil {
		return nil, nil
	}

	searchTerm := input.Subject
	if searchTerm == "" && len(input.Keywords) > 0 {
		searchTerm = input.Keywords[0]
	}
	if searchTerm == "" {
		return nil, nil
	}

	folders, err := a.s.buildArtlistFolderCandidates(ctx)
	if err != nil {
		return nil, err
	}

	terms := collectTerms(CandidatesRequest{
		Topic:    searchTerm,
		Subject:  searchTerm,
		Keywords: input.Keywords,
		Entities: input.Entities,
	})

	candidates := scoreFolderCandidates("artlist_videos.db", "artlist_folder", folders, terms, searchTerm)

	matches := make([]ScoredMatch, 0, len(candidates))
	for _, c := range candidates {
		matches = append(matches, ScoredMatch{
			Title:  c.Name,
			Path:   c.Path,
			Score:  c.Score,
			Source: c.Source,
			Link:   c.Link,
			Reason: c.Reason,
		})
	}
	return matches, nil
}
