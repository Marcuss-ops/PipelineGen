package association

import (
	"context"
	"strings"

	"velox/go-master/internal/matching"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/pkg/textutil"
)

// ArtlistStockAssociation cerca nel database delle clip di Artlist.
type ArtlistStockAssociation struct {
	svc *artlist.Service
}

func NewArtlistStockAssociation(svc *artlist.Service) *ArtlistStockAssociation {
	return &ArtlistStockAssociation{svc: svc}
}

func (a *ArtlistStockAssociation) Associate(ctx context.Context, input SegmentInput) ([]ScoredMatch, error) {
	if a.svc == nil {
		return nil, nil
	}

	searchTerm := input.Subject
	if searchTerm == "" {
		if len(input.Keywords) > 0 {
			searchTerm = input.Keywords[0]
		} else {
			return nil, nil
		}
	}

	resp, err := a.svc.Search(ctx, &artlist.SearchRequest{
		Term: searchTerm,
	})
	if err != nil {
		return nil, err
	}

	if resp == nil || len(resp.Clips) == 0 {
		return nil, nil
	}

	queryTokens := textutil.Tokenize(searchTerm)
	var matches []ScoredMatch
	for _, clip := range resp.Clips {
		targetTokens := textutil.Tokenize(clip.Name + " " + strings.Join(clip.Tags, " "))
		score := matching.CalculateTokenScore(queryTokens, targetTokens)

		if score > 30 {
			matches = append(matches, ScoredMatch{
				Title:   clip.Name,
				Path:    clip.LocalPath,
				Score:   score,
				Source:  "artlist_stock",
				Link:    clip.ExternalURL,
				Details: strings.Join(clip.Tags, ", "),
			})
		}
	}

	return matches, nil
}
