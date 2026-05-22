package matching

import "strings"

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
