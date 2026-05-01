package script

import (
	"context"
	"strings"
	"velox/go-master/internal/matching"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/pkg/textutil"
)

// DriveStockAssociation cerca cartelle nel catalogo locale dello stock
type DriveStockAssociation struct {
	dataDir string
}

func NewDriveStockAssociation(dataDir string) *DriveStockAssociation {
	return &DriveStockAssociation{dataDir: dataDir}
}

func (a *DriveStockAssociation) Associate(ctx context.Context, segment *TimelineSegment) ([]scoredMatch, error) {
	if a.dataDir == "" {
		return nil, nil
	}

	searchTerm := segmentAssociationSubject(segment)
	if searchTerm == "" {
		if len(segment.Keywords) > 0 {
			searchTerm = segment.Keywords[0]
		} else {
			return nil, nil
		}
	}

	if direct, ok, err := findDirectStockFolderCandidate(ctx, nil, a.dataDir, searchTerm, searchTerm); err == nil && ok && direct != nil {
		link := normalizeDriveFolderLink(direct.Link, direct.FolderID)
		return []scoredMatch{{
			Title:   direct.Name,
			Path:    direct.Path,
			Score:   300,
			Source:  "drive_stock",
			Link:    link,
			Details: "direct exact stock folder match",
		}}, nil
	}

	queryTokens := textutil.Tokenize(searchTerm)
	slug := strings.ReplaceAll(strings.ToLower(searchTerm), " ", "-")

	// Carichiamo il catalogo cartelle
	repo := catalog.NewRepository(a.dataDir)
	folders, err := repo.LoadStockFolders()
	if err != nil {
		return nil, err
	}

	var matches []scoredMatch
	for _, f := range folders {
		targetTokens := textutil.Tokenize(f.TopicSlug)
		score := matching.CalculateTokenScore(queryTokens, targetTokens)

		if strings.Contains(f.TopicSlug, slug) || strings.Contains(slug, f.TopicSlug) {
			score += 20
		}
		score += preferredCandidateBoost(segment, f.StockPath(), f.PickLink(), f.DisplayName())

		if score > 40 {
			matches = append(matches, scoredMatch{
				Title:  f.TopicSlug,
				Path:   f.StockPath(),
				Score:  score,
				Source: "drive_stock",
				Link:   f.PickLink(),
			})
		}
	}

	return matches, nil
}
