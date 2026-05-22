package association

import (
	"context"
	"strings"

	"velox/go-master/internal/core/scoring"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/pkg/textutil"
)

// DriveStockAssociation cerca cartelle nel catalogo locale dello stock.
type DriveStockAssociation struct {
	stockRepo   *clips.Repository
	artlistRepo *clips.Repository
}

func NewDriveStockAssociation(stockRepo, artlistRepo *clips.Repository) *DriveStockAssociation {
	return &DriveStockAssociation{
		stockRepo:   stockRepo,
		artlistRepo: artlistRepo,
	}
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

	repo := catalog.NewRepository(nil, a.stockRepo, a.artlistRepo)
	folders, err := repo.LoadStockFolders()
	if err != nil {
		return nil, err
	}

	var matches []ScoredMatch
	topicSlug := strings.ReplaceAll(strings.ToLower(input.Topic), " ", "-")

	for _, f := range folders {
		targetTokens := textutil.Tokenize(f.TopicSlug)
		score := scoring.TokenScore(queryTokens, targetTokens)

		// Exact or strong match with segment subject
		if strings.Contains(f.TopicSlug, slug) || strings.Contains(slug, f.TopicSlug) {
			score += 20
		}

		// HUGE BOOST: If the folder matches the main topic, it should be prioritized
		if topicSlug != "" && (strings.Contains(f.TopicSlug, topicSlug) || strings.Contains(topicSlug, f.TopicSlug)) {
			score += 50
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
