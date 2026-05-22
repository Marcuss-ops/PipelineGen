package association

import (
	"context"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/core/scoring"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/pkg/textutil"
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
	clipsList, err := a.repo.SearchClips(ctx, "artlist", term)
	if err != nil || len(clipsList) == 0 {
		return nil
	}

	queryTokens := textutil.Tokenize(term)
	topic = strings.ToLower(topic)

	var matches []ScoredMatch

	for _, clip := range clipsList {
		result := scoring.Calculate(scoring.Params{
			Query:       term,
			QueryTokens: queryTokens,
			Topic:       topic,
			Name:        clip.Name,
			Tags:        clip.Tags,
		})

		if result.Score > 35 {
			var matchedTokens []string
			for _, q := range queryTokens {
				for _, t := range textutil.Tokenize(strings.ToLower(clip.Name + " " + strings.Join(clip.Tags, " "))) {
					if q == t {
						matchedTokens = append(matchedTokens, q)
						break
					}
				}
			}

			a.repo.Log().Debug("Artlist match found",
				zap.String("clip", clip.Name),
				zap.Int("score", result.Score),
				zap.Bool("topic_matched", result.TopicMatched),
				zap.Strings("matched_tokens", matchedTokens))

			link := clip.DriveLink
			if link == "" {
				link = clip.ExternalURL
			}

			matches = append(matches, ScoredMatch{
				ClipID:  clip.ID,
				Title:   clip.Name,
				Path:    clip.LocalPath,
				Score:   result.Score,
				Source:  "artlist_stock",
				Link:    link,
				Details: strings.Join(clip.Tags, ", "),
				Reason:  "artlist_db: " + term,
			})
		}
	}

	return matches
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
