package association

import (
	"context"
	"encoding/json"
	"strings"

	"velox/go-master/internal/core/scoring"
	"velox/go-master/internal/repository/clips"

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
	// Search terms: combine subject, keywords, and entities
	searchTerms := a.buildSearchTerms(input)

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
		matches, err := a.searchRepo(ctx, a.artlistRepo, input.Topic, searchTerms, "artlist_clip")
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

	// Add subject as TOP PRIORITY if present
	if input.Subject != "" {
		addTerm(input.Subject)
	}

	// Add topic as PRIMARY search term
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

	return terms
}

func (a *ClipSearchAssociation) searchRepo(ctx context.Context, repo *clips.Repository, topic string, terms []string, source string) ([]ScoredMatch, error) {
	// Use up to 10 search terms
	limit := 15
	clips, err := repo.SearchClipsByKeywords(ctx, "", terms, limit)
	if err != nil {
		return nil, err
	}

	matches := make([]ScoredMatch, 0, len(clips))
	for _, clip := range clips {
		result := scoring.Calculate(scoring.Params{
			Query: strings.Join(terms, " "),
			Topic: topic,
			Name:  clip.Name,
			Path:  clip.FolderPath,
			Tags:  clip.Tags,
		})

		// STRICT FILTER: If we have a topic but it's not in the clip, and the score is low,
		// it means we are just matching generic keywords. Better to show nothing than wrong stuff.
		if topic != "" && !result.TopicMatched && result.Score < 45 {
			continue
		}

		match := ScoredMatch{
			Title:     clip.Name,
			Path:      clip.FolderPath,
			Score:     result.Score,
			Source:    source,
			Link:      clip.DriveLink,
			Reason:    "clip search match",
			Embedding: ParseEmbeddingJSON(clip.EmbeddingJSON),
		}
		if match.Link == "" && clip.FolderID != "" {
			match.Link = "https://drive.google.com/drive/folders/" + clip.FolderID
		}
		matches = append(matches, match)
	}

	return matches, nil
}

func ParseEmbeddingJSON(jsonStr string) []float32 {
	if jsonStr == "" {
		return nil
	}
	var emb []float32
	if err := json.Unmarshal([]byte(jsonStr), &emb); err != nil {
		return nil
	}
	return emb
}
