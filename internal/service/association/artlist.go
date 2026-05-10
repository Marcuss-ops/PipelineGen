package association

import (
	"context"
	"strings"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/textutil"
	"go.uber.org/zap"
)

// ArtlistStockAssociation searches in the Artlist clip database using multiple terms.
type ArtlistStockAssociation struct {
	repo *clips.Repository
}

func NewArtlistStockAssociation(repo *clips.Repository) *ArtlistStockAssociation {
	return &ArtlistStockAssociation{repo: repo}
}

func (a *ArtlistStockAssociation) Associate(ctx context.Context, input SegmentInput) ([]ScoredMatch, error) {
	if a.repo == nil {
		return nil, nil
	}

	// Generate multiple search terms
	terms := collectArtlistSearchTerms(input)

	var allMatches []ScoredMatch
	seen := make(map[string]bool)

	for _, term := range terms {
		if term == "" {
			continue
		}

		matches := a.searchInDB(ctx, term, input.Topic)
		for _, m := range matches {
			key := strings.ToLower(m.Title + "|" + m.Link)
			if !seen[key] {
				seen[key] = true
				allMatches = append(allMatches, m)
			}
		}
	}

	return allMatches, nil
}

func (a *ArtlistStockAssociation) searchInDB(ctx context.Context, term string, topic string) []ScoredMatch {
	// Use SearchClips which now falls back to LIKE when FTS5 returns 0 results
	clipsList, err := a.repo.SearchClips(ctx, term)
	if err != nil || len(clipsList) == 0 {
		return nil
	}

	queryTokens := textutil.Tokenize(term)
	topic = strings.ToLower(topic)
	topicTokens := textutil.Tokenize(topic)
	
	var matches []ScoredMatch

	for _, clip := range clipsList {
		clipText := strings.ToLower(clip.Name + " " + strings.Join(clip.Tags, " "))
		targetTokens := textutil.Tokenize(clipText)
		
		score, topicMatched, matchedTokens := a.calculateImprovedScore(queryTokens, targetTokens, clipText, topic, topicTokens)

		if score > 35 {
			a.repo.Log().Debug("Artlist match found", 
				zap.String("clip", clip.Name), 
				zap.Int("score", score), 
				zap.Bool("topic_matched", topicMatched),
				zap.Strings("matched_tokens", matchedTokens))

			matches = append(matches, ScoredMatch{
				Title:   clip.Name,
				Path:    clip.LocalPath,
				Score:   score,
				Source:  "artlist_stock",
				Link:    clip.ExternalURL,
				Details: strings.Join(clip.Tags, ", "),
				Reason:  "artlist_db: " + term,
			})
		}
	}

	return matches
}

func (a *ArtlistStockAssociation) calculateImprovedScore(queryTokens, targetTokens []string, clipText, topic string, topicTokens []string) (int, bool, []string) {
	if len(queryTokens) == 0 || len(targetTokens) == 0 {
		return 0, false, nil
	}

	matchCount := 0
	matchedTokens := make([]string, 0)
	targetMap := make(map[string]bool)
	for _, t := range targetTokens {
		targetMap[t] = true
	}

	topicMatched := false
	for _, q := range queryTokens {
		if targetMap[q] {
			matchCount++
			matchedTokens = append(matchedTokens, q)
			// Check if this matched token is part of the topic
			for _, tt := range topicTokens {
				if q == tt && len(q) > 3 {
					topicMatched = true
				}
			}
		}
	}

	if matchCount == 0 {
		return 0, false, nil
	}

	score := (matchCount * 100) / len(queryTokens)
	
	// Topic match bonus
	if topicMatched {
		score += 40
	}
	if topic != "" && strings.Contains(clipText, topic) {
		score += 50
		topicMatched = true
	}

	// MANDATORY: If topic is provided but NO topic tokens matched, cap the score
	if topic != "" && !topicMatched && score > 40 {
		score = 40
	}

	// Relevance Density Penalty (ALGORITHMIC)
	// If the clip is full of specific info that we didn't ask for, it's a "noisy" match.
	unmatchedCount := 0
	uniqueClipTokens := make(map[string]bool)
	for _, ct := range targetTokens {
		if len(ct) <= 3 {
			continue
		}
		if !uniqueClipTokens[ct] {
			uniqueClipTokens[ct] = true
			foundInQuery := false
			for _, q := range queryTokens {
				if q == ct {
					foundInQuery = true
					break
				}
			}
			if !foundInQuery {
				unmatchedCount++
			}
		}
	}

	if len(uniqueClipTokens) > 0 && !topicMatched {
		noiseRatio := float64(unmatchedCount) / float64(len(uniqueClipTokens))
		if noiseRatio > 0.6 { // Penalty for high dilution
			score -= int(noiseRatio * 50)
		}
	}

	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}

	return score, topicMatched, matchedTokens
}

// collectArtlistSearchTerms generates multiple search terms from segment input.
func collectArtlistSearchTerms(input SegmentInput) []string {
	terms := make([]string, 0)

	// Add topic (most important!)
	if input.Topic != "" {
		terms = append(terms, input.Topic)
	}

	// Add subject
	if input.Subject != "" {
		terms = append(terms, input.Subject)
	}

	// Add keywords
	terms = append(terms, input.Keywords...)

	// Add entities
	terms = append(terms, input.Entities...)

	// Extract terms from narrative
	if input.Narrative != "" {
		narrativeTerms := extractNarrativeTerms(input.Narrative)
		terms = append(terms, narrativeTerms...)
	}

	// Clean and limit
	return cleanAndLimitTerms(terms, 8)
}

func extractNarrativeTerms(narrative string) []string {
	terms := make([]string, 0)

	// Tokenize and filter stop words
	tokens := textutil.TokenizeWithStopWords(narrative)
	seen := make(map[string]bool)

	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if len(tok) < 4 {
			continue
		}
		lower := strings.ToLower(tok)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		terms = append(terms, tok)

		if len(terms) >= 5 {
			break
		}
	}

	return terms
}

func cleanAndLimitTerms(terms []string, limit int) []string {
	seen := make(map[string]bool)
	cleaned := make([]string, 0, len(terms))

	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" || len(term) < 3 {
			continue
		}
		lower := strings.ToLower(term)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		cleaned = append(cleaned, term)
		if len(cleaned) >= limit {
			break
		}
	}

	return cleaned
}
