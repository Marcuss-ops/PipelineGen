package matching

import (
	"strings"

	"velox/go-master/internal/core/scoring"
)

func CalculateTokenScore(queryTokens, targetTokens []string) int {
	return scoring.TokenScore(queryTokens, targetTokens)
}

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
