package clip

import (
	"fmt"
	"strings"

	"velox/go-master/internal/nlp"
)

// scoreClipForSentence calculates how well a clip matches a sentence
func (s *SemanticSuggester) scoreClipForSentence(clip IndexedClip, sentence string, keywords []nlp.Keyword, entities []Entity) (float64, MatchDetails) {
	var totalScore float64
	var matchType string
	var matchTerms []string
	var reasons []string

	sentenceLower := strings.ToLower(sentence)
	clipNameLower := strings.ToLower(clip.Name)
	clipPathLower := strings.ToLower(clip.FolderPath)

	// 1. ENTITY MATCH (highest priority - 100 points)
	// If sentence mentions a person/place that matches clip tags
	for _, entity := range entities {
		entityLower := strings.ToLower(entity.Value)

		// Check if entity is in clip tags
		for _, tag := range clip.Tags {
			if strings.Contains(tag, entityLower) || strings.Contains(entityLower, tag) {
				totalScore += 100
				matchType = "entity_match"
				matchTerms = append(matchTerms, fmt.Sprintf("entity:%s", entity.Value))
				reasons = append(reasons, fmt.Sprintf("Entity '%s' (%s) matches tag '%s'",
					entity.Value, entity.Type, tag))
			}
		}

		// Check if entity is in clip name
		if strings.Contains(clipNameLower, entityLower) {
			totalScore += 80
			if matchType == "" {
				matchType = "entity_in_name"
			}
			matchTerms = append(matchTerms, fmt.Sprintf("entity:%s", entity.Value))
			reasons = append(reasons, fmt.Sprintf("Entity '%s' found in clip name", entity.Value))
		}

		// Check if entity is in folder path
		if strings.Contains(clipPathLower, entityLower) {
			totalScore += 60
			if matchType == "" {
				matchType = "entity_in_folder"
			}
			matchTerms = append(matchTerms, fmt.Sprintf("entity:%s", entity.Value))
			reasons = append(reasons, fmt.Sprintf("Entity '%s' found in folder path", entity.Value))
		}
	}

	// 2. KEYWORD MATCH (medium priority - up to 50 points per keyword)
	// Fix: Use fixed points per match type, not normalized score
	for _, kw := range keywords {
		kwLower := strings.ToLower(kw.Word)

		// Check in tags (high weight for tags) - 25 points per keyword
		for _, tag := range clip.Tags {
			if strings.Contains(tag, kwLower) || strings.Contains(kwLower, tag) {
				totalScore += 25
				if matchType == "" {
					matchType = "keyword_tag_match"
				}
				if !containsString(matchTerms, kw.Word) {
					matchTerms = append(matchTerms, kw.Word)
				}
				reasons = append(reasons, fmt.Sprintf("Keyword '%s' matches tag '%s'", kw.Word, tag))
			}
		}

		// Check in clip name - 20 points
		if strings.Contains(clipNameLower, kwLower) {
			totalScore += 20
			if matchType == "" {
				matchType = "keyword_name_match"
			}
			if !containsString(matchTerms, kw.Word) {
				matchTerms = append(matchTerms, kw.Word)
			}
			reasons = append(reasons, fmt.Sprintf("Keyword '%s' found in clip name", kw.Word))
		}

		// Check in folder path - 15 points
		if strings.Contains(clipPathLower, kwLower) {
			totalScore += 15
			if matchType == "" {
				matchType = "keyword_folder_match"
			}
			if !containsString(matchTerms, kw.Word) {
				matchTerms = append(matchTerms, kw.Word)
			}
			reasons = append(reasons, fmt.Sprintf("Keyword '%s' found in folder path", kw.Word))
		}
	}

	// 3. ACTION VERB MATCH (bonus - 30 points)
	// Extract action verbs and check if they match clip context
	actionVerbs := s.extractActionVerbs(sentence)
	for _, verb := range actionVerbs {
		verbLower := strings.ToLower(verb)

		// Check if verb is implied in clip name or tags
		if strings.Contains(clipNameLower, verbLower) {
			totalScore += 30
			matchTerms = append(matchTerms, fmt.Sprintf("action:%s", verb))
			reasons = append(reasons, fmt.Sprintf("Action '%s' matches clip context", verb))
		}

		// Check in tags
		for _, tag := range clip.Tags {
			if strings.Contains(tag, verbLower) {
				totalScore += 20
				matchTerms = append(matchTerms, fmt.Sprintf("action:%s", verb))
			}
		}
	}

	// 4. EXACT PHRASE MATCH (bonus - 50 points)
	// If the entire sentence or large phrase matches clip name
	if len(sentenceLower) > 5 && strings.Contains(clipNameLower, sentenceLower) {
		totalScore += 50
		matchType = "phrase_match"
		reasons = append(reasons, "Large phrase match in clip name")
	}

	// 5. GROUP MATCH (small bonus - 15 points)
	// Prefer clips from relevant groups
	groupKeywords := s.detectGroupFromSentence(sentence)
	if groupKeywords != "" && strings.EqualFold(clip.Group, groupKeywords) {
		totalScore += 15
		reasons = append(reasons, fmt.Sprintf("Group match: %s", groupKeywords))
	}

	// Normalize score to 0-100
	if totalScore > 100 {
		totalScore = 100
	}

	if matchType == "" {
		matchType = "none"
	}

	return totalScore, MatchDetails{
		MatchType:  matchType,
		MatchTerms: matchTerms,
		Reason:     strings.Join(reasons, "; "),
	}
}
