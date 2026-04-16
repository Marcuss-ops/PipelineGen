package clip

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// DefaultMinScore is the default minimum score for suggestions
const DefaultMinScore = 20.0

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
	s := strings.TrimSpace(sentence)
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
	hashStr := fmt.Sprintf("%x", hash[:8])
	return fmt.Sprintf("%s:%s:%d:%.0f:%s", prefix, hashStr, maxResults, minScore, mediaType)
}
