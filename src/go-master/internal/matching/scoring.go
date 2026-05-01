package matching

import (
	"path/filepath"
	"strings"
	"velox/go-master/pkg/textutil"
)

// ScoreAsset calculates a match score (0-100) between a search phrase and an asset
func ScoreAsset(phrase string, name, filename, folder, tags string, cfg ScoringConfig) (float64, string) {
	phraseNorm := textutil.Normalize(phrase)
	if phraseNorm == "" {
		return 0, ""
	}

	phraseTokens := textutil.Tokenize(phraseNorm)
	if len(phraseTokens) == 0 {
		return 0, ""
	}

	candidateText := strings.Join([]string{name, filename, folder, tags}, " ")
	candidateNorm := textutil.Normalize(candidateText)
	if candidateNorm == "" {
		return 0, ""
	}

	candidateTokens := textutil.Tokenize(candidateNorm)
	candidateSet := make(map[string]struct{}, len(candidateTokens))
	for _, tok := range candidateTokens {
		candidateSet[tok] = struct{}{}
	}

	hits := 0
	for _, tok := range phraseTokens {
		if _, ok := candidateSet[tok]; ok {
			hits++
		}
	}

	base := float64(hits) / float64(len(phraseTokens)) * 100
	if base <= 0 {
		return 0, ""
	}

	boost := 0.0
	nameNorm := textutil.Normalize(name)
	fileNorm := textutil.Normalize(strings.TrimSuffix(filename, filepath.Ext(filename)))

	if nameNorm != "" && strings.Contains(phraseNorm, nameNorm) {
		boost += cfg.NameMatchBoost
	} else if fileNorm != "" && strings.Contains(phraseNorm, fileNorm) {
		boost += cfg.FilenameMatchBoost
	}

	score := base + boost
	if score > 100 {
		score = 100
	}

	reason := "token_overlap"
	if boost > 0 {
		reason += "+boost"
	}

	return score, reason
}

// CalculateTokenScore calculates a similarity score (0-100) between two sets of tokens.
func CalculateTokenScore(queryTokens, targetTokens []string) int {
	if len(queryTokens) == 0 || len(targetTokens) == 0 {
		return 0
	}

	matches := 0
	targetMap := make(map[string]bool)
	for _, t := range targetTokens {
		targetMap[t] = true
	}

	for _, q := range queryTokens {
		if targetMap[q] {
			matches++
		}
	}

	if matches == 0 {
		return 0
	}

	score := (matches * 100) / len(queryTokens)
	queryJoined := strings.Join(queryTokens, " ")
	targetJoined := strings.Join(targetTokens, " ")
	if strings.Contains(queryJoined, targetJoined) {
		score += 30
	}

	if score > 100 {
		score = 100
	}

	return score
}

// ScoreText returns a simple count of how many terms appear in the candidate text
func ScoreText(candidate string, terms []string) int {
	candidate = strings.ToLower(candidate)
	score := 0
	for _, term := range terms {
		if term == "" {
			continue
		}
		if strings.Contains(candidate, term) {
			score++
		}
	}
	return score
}
