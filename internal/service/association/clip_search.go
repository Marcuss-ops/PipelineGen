package association

import (
	"context"
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
	clips, err := repo.SearchClipsByKeywords(ctx, terms, limit)
	if err != nil {
		return nil, err
	}

	matches := make([]ScoredMatch, 0, len(clips))
	for _, clip := range clips {
		score, topicMatched := a.calculateScore(clip, topic, terms)
		
		// STRICT FILTER: If we have a topic but it's not in the clip, and the score is low,
		// it means we are just matching generic keywords. Better to show nothing than wrong stuff.
		if topic != "" && !topicMatched && score < 40 {
			continue
		}

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

func (a *ClipSearchAssociation) calculateScore(clip *models.Clip, topic string, terms []string) (int, bool) {
	score := 10 // Lower base score to allow for more differentiation

	clipName := strings.ToLower(clip.Name)
	clipPath := strings.ToLower(clip.FolderPath)
	clipTags := strings.ToLower(strings.Join(clip.Tags, " "))
	
	topic = strings.ToLower(topic)
	topicMatched := false

	// 1. Topic Boost (MASSIVE)
	if topic != "" {
		topicWords := strings.Fields(topic)
		for _, tw := range topicWords {
			if len(tw) <= 3 {
				continue
			}
			if strings.Contains(clipName, tw) || strings.Contains(clipTags, tw) {
				score += 50
				topicMatched = true
			}
		}
		if strings.Contains(clipName, topic) || strings.Contains(clipTags, topic) {
			score += 100 // Exact topic match is gold
			topicMatched = true
		}
	}

	// 2. Term Matches
	for i, term := range terms {
		term = strings.ToLower(term)
		weight := 10
		if i < 2 {
			weight = 20 // First two terms (usually subject/topic) have more weight
		}

		matched := false
		if strings.Contains(clipName, term) {
			score += weight * 2
			matched = true
		}
		if strings.Contains(clipPath, term) {
			score += weight
			matched = true
		}
		if strings.Contains(clipTags, term) {
			score += weight
			matched = true
		}
		
		if matched && topicMatched {
			score += 10 // Synergy bonus
		}
	}

	// 3. Generic Filtering (PENALTY)
	// If it's a "Surf" clip and we didn't mention surf, penalize it if it's just a "Sea" match
	if !strings.Contains(topic, "surf") && strings.Contains(clipName, "surf") {
		score -= 50
	}

	return score, topicMatched
}
