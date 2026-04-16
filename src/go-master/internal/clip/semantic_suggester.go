// Package clip provides clip management functionality for the VeloxEditing system.
package clip

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/nlp"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// SemanticSuggester provides intelligent clip suggestions based on script/context matching
type SemanticSuggester struct {
	indexer *Indexer
}

// NewSemanticSuggester creates a new semantic suggester
func NewSemanticSuggester(indexer *Indexer) *SemanticSuggester {
	return &SemanticSuggester{
		indexer: indexer,
	}
}

// SuggestForSentence finds clips that match a sentence from the script
// Combines Drive clips + Artlist clips for unified suggestions
func (s *SemanticSuggester) SuggestForSentence(ctx context.Context, sentence string, maxResults int, minScore float64, mediaType string) []SuggestionResult {
	startTime := time.Now()

	// Normalize sentence for consistent caching
	normalizedSentence := normalizeSentence(sentence)
	// Include mediaType in cache key to avoid returning wrong media type from cache
	cacheKey := buildCacheKey("sentence", normalizedSentence, maxResults, minScore, mediaType)

	// Use default min score if not specified
	if minScore <= 0 {
		minScore = DefaultMinScore
	}

	if cached, found := s.indexer.GetCache().Get(cacheKey); found {
		if results, ok := cached.([]SuggestionResult); ok {
			return results
		}
	}

	// Get Drive clips
	index := s.indexer.GetIndex()
	if index == nil {
		index = &ClipIndex{}
	}

	// Extract keywords and entities from sentence
	keywords := nlp.ExtractKeywords(sentence, 20)
	entities := s.extractEntities(sentence)

	logger.Debug("Extracted keywords for sentence",
		zap.String("sentence", sentence),
		zap.Int("keywords", len(keywords)),
		zap.Int("entities", len(entities)))

	// Score Drive clips
	var suggestions []SuggestionResult

	for _, clip := range index.Clips {
		// Filter by media type if specified
		if mediaType != "" && !strings.EqualFold(clip.MediaType, mediaType) {
			continue
		}
		score, matchDetails := s.scoreClipForSentence(clip, sentence, keywords, entities)

		if score >= minScore {
			suggestions = append(suggestions, SuggestionResult{
				Clip:        clip,
				Score:       score,
				MatchType:   matchDetails.MatchType,
				MatchTerms:  matchDetails.MatchTerms,
				MatchReason: matchDetails.Reason,
			})
		}
	}

	// Also search Artlist if available
	if s.indexer.artlistSrc != nil {
		artlistClips, err := s.indexer.artlistSrc.SearchClips(sentence, maxResults)
		if err == nil {
			for _, clip := range artlistClips {
				score, matchDetails := s.scoreClipForSentence(clip, sentence, keywords, entities)

				if score >= minScore {
					suggestions = append(suggestions, SuggestionResult{
						Clip:        clip,
						Score:       score,
						MatchType:   matchDetails.MatchType,
						MatchTerms:  matchDetails.MatchTerms,
						MatchReason: matchDetails.Reason,
					})
				}
			}
		}
	}

	// Sort by score descending
	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Score > suggestions[j].Score
	})

	// Apply usage penalty to penalize recently/overused clips
	for i := range suggestions {
		penalty := GlobalUsageTracker.GetPenalty(suggestions[i].Clip.ID)
		if penalty > 0 {
			suggestions[i].Score -= penalty
			if suggestions[i].Score < 0 {
				suggestions[i].Score = 0
			}
			suggestions[i].MatchReason += fmt.Sprintf("; usage_penalty:-%.0f", penalty)
		}
	}

	// Re-sort after penalty application
	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Score > suggestions[j].Score
	})

	// Limit results
	if maxResults > 0 && len(suggestions) > maxResults {
		suggestions = suggestions[:maxResults]
	}

	// Fallback: if no clips meet threshold, return generic clips
	if len(suggestions) == 0 {
		suggestions = s.getFallbackClips(sentence, maxResults, minScore, mediaType)
	}

	// Record usage for suggested clips (so future calls penalize them)
	var clipIDs []string
	for _, s := range suggestions {
		clipIDs = append(clipIDs, s.Clip.ID)
	}
	GlobalUsageTracker.RecordMultipleUsage(clipIDs)

	// Cache results AFTER usage recording so penalties are reflected
	s.indexer.GetCache().Set(cacheKey, suggestions)

	logger.Info("Semantic suggestion completed",
		zap.String("sentence", sentence),
		zap.Int("suggestions", len(suggestions)),
		zap.Duration("duration", time.Since(startTime)))

	return suggestions
}

// SuggestForScript finds clips for an entire script, sentence by sentence
func (s *SemanticSuggester) SuggestForScript(ctx context.Context, script string, maxResultsPerSentence int, minScore float64, mediaType string) []ScriptSuggestion {
	startTime := time.Now()

	// Split script into sentences
	sentences := s.splitIntoSentences(script)

	logger.Info("Processing script for clip suggestions",
		zap.Int("sentences", len(sentences)),
		zap.String("media_type", mediaType))

	// Process sentences in parallel
	type sentenceResult struct {
		index       int
		sentence    string
		suggestions []SuggestionResult
	}

	resultsCh := make(chan sentenceResult, len(sentences))

	// Use a wait group to process all sentences concurrently
	var wg sync.WaitGroup
	for i, sentence := range sentences {
		if len(sentence) < 10 { // Skip very short sentences
			continue
		}

		wg.Add(1)
		go func(idx int, sent string) {
			defer wg.Done()
			suggestions := s.SuggestForSentence(ctx, sent, maxResultsPerSentence, minScore, mediaType)
			resultsCh <- sentenceResult{index: idx, sentence: sent, suggestions: suggestions}
		}(i, sentence)
	}

	// Close channel when all goroutines complete
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	// Collect and order results
	resultsMap := make(map[int]sentenceResult)
	for result := range resultsCh {
		resultsMap[result.index] = result
	}

	// Build ordered script suggestions
	var scriptSuggestions []ScriptSuggestion
	for i, sentence := range sentences {
		if len(sentence) < 10 {
			continue
		}
		if result, ok := resultsMap[i]; ok && len(result.suggestions) > 0 {
			scriptSuggestions = append(scriptSuggestions, ScriptSuggestion{
				Sentence:    sentence,
				Suggestions: result.suggestions,
				BestScore:   result.suggestions[0].Score,
			})
		}
	}

	logger.Info("Script processing completed",
		zap.Int("sentences_processed", len(sentences)),
		zap.Int("sentences_with_matches", len(scriptSuggestions)),
		zap.Duration("duration", time.Since(startTime)))

	return scriptSuggestions
}

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

// extractEntities extracts named entities from text
func (s *SemanticSuggester) extractEntities(text string) []Entity {
	var entities []Entity

	// Simple entity extraction based on patterns
	// In production, this would use spaCy or similar

	// Person names: Capitalized words (simple heuristic)
	words := strings.Fields(text)
	for i, word := range words {
		cleaned := strings.Trim(word, ".,!?;:\"'()[]{}")

		// Check if it's a capitalized word (potential name)
		if len(cleaned) > 1 && cleaned[0] >= 'A' && cleaned[0] <= 'Z' {
			// Check if next word is also capitalized (full name pattern)
			if i+1 < len(words) {
				nextWord := strings.Trim(words[i+1], ".,!?;:\"'()[]{}")
				if len(nextWord) > 1 && nextWord[0] >= 'A' && nextWord[0] <= 'Z' {
					fullName := cleaned + " " + nextWord
					entities = append(entities, Entity{
						Value: fullName,
						Type:  "PERSON",
					})
					i++ // Skip next word
					continue
				}
			}

			// Single capitalized word
			if len(cleaned) > 2 {
				entities = append(entities, Entity{
					Value: cleaned,
					Type:  "PERSON_OR_PLACE",
				})
			}
		}
	}

	return entities
}

// extractActionVerbs extracts action verbs from sentence
func (s *SemanticSuggester) extractActionVerbs(sentence string) []string {
	var verbs []string

	// Common action verbs in Italian and English
	actionVerbs := []string{
		"saluta", "greet", "greets", "greeting",
		"parla", "talk", "talks", "speaking", "speak",
		"cammina", "walk", "walks", "walking",
		"corre", "run", "runs", "running",
		"guida", "drive", "drives", "driving",
		"spiega", "explain", "explains", "explaining",
		"mostra", "show", "shows", "showing",
		"presenta", "present", "presents", "presenting",
		"dimostra", "demonstrate", "demonstrates",
		"intervista", "interview", "interviews",
		"risponde", "answer", "answers", "answering",
		"chiede", "ask", "asks", "asking",
		"ride", "laugh", "laughs", "laughing",
		"sorride", "smile", "smiles", "smiling",
		"stringe", "shake", "shakes", "handshake",
		"abbraccia", "hug", "hugs", "hugging",
		"balla", "dance", "dances", "dancing",
		"canta", "sing", "sings", "singing",
	}

	sentenceLower := strings.ToLower(sentence)

	for _, verb := range actionVerbs {
		if strings.Contains(sentenceLower, verb) {
			verbs = append(verbs, verb)
		}
	}

	return verbs
}

// detectGroupFromSentence detects the group from sentence context
func (s *SemanticSuggester) detectGroupFromSentence(sentence string) string {
	sentenceLower := strings.ToLower(sentence)

	switch {
	case strings.Contains(sentenceLower, "intervista") || strings.Contains(sentenceLower, "interview"):
		return "interviews"
	case strings.Contains(sentenceLower, "natura") || strings.Contains(sentenceLower, "nature"):
		return "nature"
	case strings.Contains(sentenceLower, "tecnologia") || strings.Contains(sentenceLower, "tech"):
		return "tech"
	case strings.Contains(sentenceLower, "business") || strings.Contains(sentenceLower, "azienda"):
		return "business"
	case strings.Contains(sentenceLower, "città") || strings.Contains(sentenceLower, "city"):
		return "urban"
	default:
		return ""
	}
}

// getFallbackClips returns generic/b-roll clips when no specific match is found
// This ensures the system always returns something useful, even for unknown topics
// Respects mediaType filter: if "clip" or "stock", only returns clips of that type
func (s *SemanticSuggester) getFallbackClips(sentence string, maxResults int, _ float64, mediaType string) []SuggestionResult {
	index := s.indexer.GetIndex()
	if index == nil || len(index.Clips) == 0 {
		return nil
	}

	// Priority order for fallback groups
	fallbackGroups := []string{"broll", "general", "stock"}

	var fallbackClips []IndexedClip
	for _, group := range fallbackGroups {
		for _, clip := range index.Clips {
			// Filter by media type if specified
			if mediaType != "" && !strings.EqualFold(clip.MediaType, mediaType) {
				continue
			}
			if strings.EqualFold(clip.Group, group) {
				// Skip heavily used clips (usage penalty > 15)
				if GlobalUsageTracker.GetPenalty(clip.ID) > 15 {
					continue
				}
				fallbackClips = append(fallbackClips, clip)
				if len(fallbackClips) >= maxResults {
					break
				}
			}
		}
		if len(fallbackClips) >= maxResults {
			break
		}
	}

	if len(fallbackClips) == 0 {
		return nil
	}

	// Convert to SuggestionResult with low scores
	var results []SuggestionResult
	for _, clip := range fallbackClips {
		results = append(results, SuggestionResult{
			Clip:        clip,
			Score:       5, // Very low score to indicate fallback
			MatchType:   "fallback_generic",
			MatchTerms:  []string{"generic"},
			MatchReason: "Generic fallback clip (no specific match found)",
		})
	}

	logger.Debug("Fallback clips returned",
		zap.Int("count", len(results)),
		zap.Strings("groups_used", fallbackGroups))

	return results
}

// splitIntoSentences splits text into sentences
func (s *SemanticSuggester) splitIntoSentences(text string) []string {
	// Simple sentence splitting
	// In production, use proper NLP sentence segmentation

	var sentences []string
	var current strings.Builder

	for _, char := range text {
		current.WriteRune(char)

		// Check for sentence ending
		if char == '.' || char == '!' || char == '?' {
			sentence := strings.TrimSpace(current.String())
			if len(sentence) > 0 {
				sentences = append(sentences, sentence)
			}
			current.Reset()
		}
	}

	// Add remaining text
	remaining := strings.TrimSpace(current.String())
	if len(remaining) > 0 {
		sentences = append(sentences, remaining)
	}

	return sentences
}

// SuggestionResult represents a single clip suggestion with metadata
type SuggestionResult struct {
	Clip        IndexedClip `json:"clip"`
	Score       float64     `json:"score"`
	MatchType   string      `json:"match_type"`
	MatchTerms  []string    `json:"match_terms"`
	MatchReason string      `json:"match_reason"`
}

// ScriptSuggestion represents suggestions for a script sentence
type ScriptSuggestion struct {
	Sentence    string             `json:"sentence"`
	Suggestions []SuggestionResult `json:"suggestions"`
	BestScore   float64            `json:"best_score"`
}

// MatchDetails holds details about why a clip matched
type MatchDetails struct {
	MatchType  string
	MatchTerms []string
	Reason     string
}

// Entity represents a named entity in text
type Entity struct {
	Value string
	Type  string // PERSON, PLACE, ORGANIZATION, etc.
}

// containsString checks if a string slice contains a string
func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, item) {
			return true
		}
	}
	return false
}

// normalizeSentence normalizes whitespace and trims for consistent cache keys
func normalizeSentence(sentence string) string {
	// Trim whitespace
	s := strings.TrimSpace(sentence)
	// Normalize internal whitespace: collapse multiple spaces into one
	var result strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				result.WriteRune(' ')
				prevSpace = true
			}
		} else {
			result.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(result.String())
}

// buildCacheKey creates a compact cache key using SHA256 hash for long strings
func buildCacheKey(prefix, text string, maxResults int, minScore float64, mediaType string) string {
	hash := sha256.Sum256([]byte(text))
	hashStr := fmt.Sprintf("%x", hash[:8]) // Use first 8 bytes for uniqueness
	return fmt.Sprintf("%s:%s:%d:%.0f:%s", prefix, hashStr, maxResults, minScore, mediaType)
}
