package matching

import (
	"strings"
	"path/filepath"
)

// ScoreAsset calculates a match score (0-100) between a search phrase and an asset
func ScoreAsset(phrase string, name, filename, folder, tags string, cfg ScoringConfig) (float64, string) {
	phraseNorm := Normalize(phrase)
	if phraseNorm == "" {
		return 0, ""
	}

	phraseTokens := Tokenize(phraseNorm)
	if len(phraseTokens) == 0 {
		return 0, ""
	}

	candidateText := strings.Join([]string{name, filename, folder, tags}, " ")
	candidateNorm := Normalize(candidateText)
	if candidateNorm == "" {
		return 0, ""
	}

	candidateTokens := Tokenize(candidateNorm)
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
	nameNorm := Normalize(name)
	fileNorm := Normalize(strings.TrimSuffix(filename, filepath.Ext(filename)))

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
