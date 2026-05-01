package association

import (
	"context"
	"strings"

	"velox/go-master/internal/matching"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/pkg/textutil"
)

// DriveStockAssociation cerca cartelle nel catalogo locale dello stock.
type DriveStockAssociation struct {
	dataDir string
}

func NewDriveStockAssociation(dataDir string) *DriveStockAssociation {
	return &DriveStockAssociation{dataDir: dataDir}
}

func (a *DriveStockAssociation) Associate(ctx context.Context, input SegmentInput) ([]ScoredMatch, error) {
	searchTerm := input.Subject
	if searchTerm == "" && len(input.Keywords) > 0 {
		searchTerm = input.Keywords[0]
	}
	if searchTerm == "" {
		return nil, nil
	}

	queryTokens := textutil.Tokenize(searchTerm)
	slug := strings.ReplaceAll(strings.ToLower(searchTerm), " ", "-")

	repo := catalog.NewRepository(a.dataDir)
	folders, err := repo.LoadStockFolders()
	if err != nil {
		return nil, err
	}

	var matches []ScoredMatch
	for _, f := range folders {
		targetTokens := textutil.Tokenize(f.TopicSlug)
		score := matching.CalculateTokenScore(queryTokens, targetTokens)

		if strings.Contains(f.TopicSlug, slug) || strings.Contains(slug, f.TopicSlug) {
			score += 20
		}

		if score > 40 {
			link := f.DriveLink
			if link == "" && f.FolderID != "" {
				link = "https://drive.google.com/drive/folders/" + f.FolderID
			}
			matches = append(matches, ScoredMatch{
				Title:  f.TopicSlug,
				Path:   f.StockPath(),
				Score:  score,
				Source: "drive_stock",
				Link:   link,
			})
		}
	}

	return matches, nil
}
