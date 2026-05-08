package script

import (
	"strings"

	"go.uber.org/zap"
)

// isSemanticallyRelevant checks if the segment's matches are actually relevant to the topic/subject.
// Returns false if matches are based on weak keywords (e.g., "rain", "net") while ignoring the main topic.
func isSemanticallyRelevant(seg TimelineSegment, topic string) bool {
	// Collect all match titles/paths
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

	// Build a comprehensive set of relevant terms from multiple sources
	relevantTerms := buildRelevantTerms(seg, topic)

	// Build a set of terms from search suggestions (these are the queries that found the matches)
	searchTerms := make(map[string]bool)
	for _, s := range seg.SearchSuggestions {
		for _, term := range strings.Fields(strings.ToLower(s)) {
			if len(term) > 2 && !isStopWord(term) {
				searchTerms[term] = true
			}
		}
	}

	// Check each match for relevance
	relevantMatchCount := 0
	for _, match := range allMatches {
		if isMatchRelevant(match, relevantTerms, searchTerms) {
			relevantMatchCount++
		}
	}

	// At least some matches must be relevant
	if relevantMatchCount == 0 {
		zap.L().Warn("semantic mismatch detected - no relevant matches",
			zap.String("topic", topic),
			zap.String("subject", seg.Subject),
			zap.String("visual_subject", seg.VisualSubject),
			zap.Strings("search_suggestions", seg.SearchSuggestions),
			zap.Strings("matches", allMatches),
		)
		return false
	}

	return true
}

// buildRelevantTerms builds a comprehensive set of terms from segment data
func buildRelevantTerms(seg TimelineSegment, topic string) map[string]bool {
	terms := make(map[string]bool)

	// Add terms from topic
	for _, term := range strings.Fields(strings.ToLower(topic)) {
		if len(term) > 2 && !isStopWord(term) {
			terms[term] = true
		}
	}

	// Add terms from subject (more specific than topic)
	for _, term := range strings.Fields(strings.ToLower(seg.Subject)) {
		if len(term) > 2 && !isStopWord(term) {
			terms[term] = true
		}
	}

	// Add terms from visual subject
	if seg.VisualSubject != "" {
		for _, term := range strings.Fields(strings.ToLower(seg.VisualSubject)) {
			if len(term) > 2 && !isStopWord(term) {
				terms[term] = true
			}
		}
	}

	// Add terms from keywords
	for _, kw := range seg.Keywords {
		for _, term := range strings.Fields(strings.ToLower(kw)) {
			if len(term) > 2 && !isStopWord(term) {
				terms[term] = true
			}
		}
	}

	// Add terms from entities
	for _, ent := range seg.Entities {
		for _, term := range strings.Fields(strings.ToLower(ent)) {
			if len(term) > 2 && !isStopWord(term) {
				terms[term] = true
			}
		}
	}

	return terms
}

// isMatchRelevant checks if a match is relevant based on relevant terms and search terms
func isMatchRelevant(match string, relevantTerms, searchTerms map[string]bool) bool {
	matchLower := strings.ToLower(match)

	// Check if match contains any relevant term
	for term := range relevantTerms {
		if strings.Contains(matchLower, term) {
			return true
		}
	}

	// Check if match contains any search term (these are the queries used to find the match)
	for term := range searchTerms {
		if strings.Contains(matchLower, term) {
			return true
		}
	}

	return false
}

// hasUsefulVisualMatch checks if the segment has matches that align with visual_subject and search_suggestions.
// Returns true only if matches exist and are relevant to the visual context.
func hasUsefulVisualMatch(seg TimelineSegment, topic string) bool {
	// Collect all matches
	allMatches := make([]string, 0)
	for _, m := range seg.StockMatches {
		allMatches = append(allMatches, m.Title, m.Path)
	}
	for _, m := range seg.ArtlistMatches {
		allMatches = append(allMatches, m.Title, m.Path)
	}

	// If no matches, clearly not useful
	if len(allMatches) == 0 {
		return false
	}

	// Build visual context from visual_subject and search_suggestions
	visualTerms := make(map[string]bool)

	// Add terms from visual_subject
	if seg.VisualSubject != "" {
		for _, term := range strings.Fields(strings.ToLower(seg.VisualSubject)) {
			if len(term) > 2 {
				visualTerms[term] = true
			}
		}
	}

	// Add terms from search_suggestions (these are the intended search queries)
	for _, s := range seg.SearchSuggestions {
		for _, term := range strings.Fields(strings.ToLower(s)) {
			if len(term) > 2 {
				visualTerms[term] = true
			}
		}
	}

	// Add terms from subject
	for _, term := range strings.Fields(strings.ToLower(seg.Subject)) {
		if len(term) > 2 {
			visualTerms[term] = true
		}
	}

	// If we have no visual context, reject matches (they're likely irrelevant)
	if len(visualTerms) == 0 {
		zap.L().Warn("no visual context available, rejecting matches",
			zap.Int("segment_index", seg.Index),
			zap.String("subject", seg.Subject),
		)
		return false
	}

	// Check if at least one match contains visual terms
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

	// At least some matches must be relevant
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
