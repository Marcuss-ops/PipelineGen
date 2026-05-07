package association

import (
	"context"
	"fmt"
	"strings"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/models"

	"go.uber.org/zap"
)

// ClipSearchAssociation searches individual Artlist clips from the artlist repository.
type ClipSearchAssociation struct {
	artlistRepo *clips.Repository
}

func NewClipSearchAssociation(artlistRepo *clips.Repository) *ClipSearchAssociation {
	return &ClipSearchAssociation{
		artlistRepo: artlistRepo,
	}
}

func (a *ClipSearchAssociation) Associate(ctx context.Context, input SegmentInput) ([]ScoredMatch, error) {
	fmt.Println("DEBUG: ClipSearchAssociation.Associate() called")
	// Search terms: combine subject, keywords, and entities
	searchTerms := a.buildSearchTerms(input)
	fmt.Println("DEBUG: searchTerms =", searchTerms)

	zap.L().Info("ClipSearchAssociation: searching artlist clips",
		zap.String("topic", input.Topic),
		zap.String("subject", input.Subject),
		zap.Strings("search_terms", searchTerms),
	)

	if len(searchTerms) == 0 {
		return nil, nil
	}

	// Search ONLY in artlist repository
	if a.artlistRepo != nil {
		matches, err := a.searchRepo(ctx, a.artlistRepo, searchTerms, "artlist_clip")
		fmt.Println("DEBUG: ClipSearchAssociation search results: matches =", len(matches), "err =", err)
		zap.L().Info("ClipSearchAssociation: search results",
			zap.Int("match_count", len(matches)),
			zap.Error(err),
		)
		return matches, err
	}

	return nil, nil
}

func (a *ClipSearchAssociation) buildSearchTerms(input SegmentInput) []string {
	seen := make(map[string]bool)
	var terms []string

	addTerm := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" || seen[term] {
			return
		}
		seen[term] = true
		terms = append(terms, term)
	}

	// Add topic as PRIMARY search term (artlist clips are tagged by topic)
	if input.Topic != "" {
		addTerm(input.Topic)
		// Also add individual words from topic
		words := strings.Fields(input.Topic)
		for _, w := range words {
			if len(w) > 3 {
				addTerm(w)
			}
		}
	}

	// Add keywords
	for _, kw := range input.Keywords {
		addTerm(kw)
	}

	// Add subject as secondary search term
	if input.Subject != "" {
		addTerm(input.Subject)
	}

	return terms
}

func (a *ClipSearchAssociation) searchRepo(ctx context.Context, repo *clips.Repository, terms []string, source string) ([]ScoredMatch, error) {
	// Use up to 5 search terms
	limit := 10
	clips, err := repo.SearchClipsByKeywords(ctx, terms, limit)
	if err != nil {
		return nil, err
	}

	matches := make([]ScoredMatch, 0, len(clips))
	for _, clip := range clips {
		score := a.calculateScore(clip, terms)
		match := ScoredMatch{
			Title:  clip.Name,
			Path:   clip.FolderPath,
			Score:  score,
			Source: source,
			Link:   clip.DriveLink,
			Reason: "clip search match",
		}
		if match.Link == "" && clip.FolderID != "" {
			match.Link = "https://drive.google.com/drive/folders/" + clip.FolderID
		}
		matches = append(matches, match)
	}

	return matches, nil
}

func (a *ClipSearchAssociation) calculateScore(clip *models.Clip, terms []string) int {
	score := 50 // Base score for clip matches

	// Boost score if clip name matches terms
	clipName := strings.ToLower(clip.Name)
	clipPath := strings.ToLower(clip.FolderPath)
	clipTags := strings.ToLower(strings.Join(clip.Tags, " "))

	for _, term := range terms {
		term = strings.ToLower(term)
		if strings.Contains(clipName, term) {
			score += 20
		}
		if strings.Contains(clipPath, term) {
			score += 15
		}
		if strings.Contains(clipTags, term) {
			score += 10
		}
	}

	return score
}
