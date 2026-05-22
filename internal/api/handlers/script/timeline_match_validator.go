package script

import (
	"strings"

	"go.uber.org/zap"
)

func hasUsefulVisualMatch(seg TimelineSegment, topic string) bool {
	allMatches := make([]string, 0)
	for _, m := range seg.StockMatches {
		allMatches = append(allMatches, m.Title, m.Path)
	}
	for _, m := range seg.ArtlistMatches {
		allMatches = append(allMatches, m.Title, m.Path)
	}

	if len(allMatches) == 0 {
		return false
	}

	visualTerms := make(map[string]bool)

	if seg.VisualSubject != "" {
		for _, term := range strings.Fields(strings.ToLower(seg.VisualSubject)) {
			if len(term) > 2 {
				visualTerms[term] = true
			}
		}
	}

	for _, s := range seg.SearchSuggestions {
		for _, term := range strings.Fields(strings.ToLower(s)) {
			if len(term) > 2 {
				visualTerms[term] = true
			}
		}
	}

	for _, term := range strings.Fields(strings.ToLower(seg.Subject)) {
		if len(term) > 2 {
			visualTerms[term] = true
		}
	}

	if len(visualTerms) == 0 {
		zap.L().Warn("no visual context available, rejecting matches",
			zap.Int("segment_index", seg.Index),
			zap.String("subject", seg.Subject),
		)
		return false
	}

	relevantCount := 0
	for _, match := range allMatches {
		matchLower := strings.ToLower(match)
		for term := range visualTerms {
			if strings.Contains(matchLower, term) {
				relevantCount++
				break
			}
		}
	}

	if relevantCount == 0 {
		zap.L().Warn("no matches align with visual context",
			zap.Int("segment_index", seg.Index),
			zap.String("visual_subject", seg.VisualSubject),
			zap.Strings("search_suggestions", seg.SearchSuggestions),
			zap.Strings("matches", allMatches),
		)
		return false
	}

	return true
}

func isStopWord(word string) bool {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "was": true,
		"were": true, "are": true, "been": true, "have": true, "has": true,
		"had": true, "but": true, "and": true, "or": true, "then": true,
		"they": true, "their": true, "for": true, "with": true, "this": true,
		"that": true, "these": true, "those": true, "of": true, "to": true,
		"in": true, "on": true, "at": true, "by": true, "from": true,
	}
	return stopWords[word]
}
