package scoring

import (
	"strings"

	"velox/go-master/internal/pkg/textutil"
)

type Params struct {
	Query       string
	QueryTokens []string
	Topic       string
	Name        string
	Path        string
	Tags        []string
}

type Result struct {
	Score        int
	TopicMatched bool
}

func TokenScore(queryTokens, targetTokens []string) int {
	if len(queryTokens) == 0 || len(targetTokens) == 0 {
		return 0
	}

	targetMap := make(map[string]bool)
	for _, t := range targetTokens {
		targetMap[t] = true
	}

	matches := 0
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

func Calculate(p Params) Result {
	queryTokens := p.QueryTokens
	if len(queryTokens) == 0 && p.Query != "" {
		queryTokens = textutil.Tokenize(p.Query)
	}

	if len(queryTokens) == 0 {
		return Result{}
	}

	topic := strings.TrimSpace(strings.ToLower(p.Topic))
	name := strings.ToLower(p.Name)
	path := strings.ToLower(p.Path)
	tagsStr := strings.ToLower(strings.Join(p.Tags, " "))

	targetText := name + " " + tagsStr
	targetTokens := textutil.Tokenize(targetText)

	tokenScore := TokenScore(queryTokens, targetTokens)

	targetMap := make(map[string]bool)
	for _, t := range targetTokens {
		targetMap[t] = true
	}

	topicMatched := false
	if topic != "" {
		topicTokens := textutil.Tokenize(topic)
		for _, tt := range topicTokens {
			if len(tt) <= 3 {
				continue
			}
			if strings.Contains(name, tt) || strings.Contains(tagsStr, tt) {
				topicMatched = true
				break
			}
		}
		if !topicMatched && (strings.Contains(name, topic) || strings.Contains(tagsStr, topic)) {
			topicMatched = true
		}
	}

	score := tokenScore

	if len(p.Query) > 3 && (strings.Contains(name, strings.ToLower(p.Query)) || strings.Contains(path, strings.ToLower(p.Query))) {
		score += 15
	}

	if topicMatched {
		score += 40
		if strings.Contains(name, topic) || strings.Contains(tagsStr, topic) {
			score += 50
		}
	}

	if topic != "" && !topicMatched && score > 40 {
		score = 40
	}

	if !topicMatched {
		unmatchedCount := 0
		uniqueTokens := make(map[string]bool)
		for _, ct := range targetTokens {
			if len(ct) <= 3 {
				continue
			}
			if !uniqueTokens[ct] {
				uniqueTokens[ct] = true
				found := false
				for _, q := range queryTokens {
					if q == ct {
						found = true
						break
					}
				}
				if !found {
					unmatchedCount++
				}
			}
		}

		if len(uniqueTokens) > 0 {
			noiseRatio := float64(unmatchedCount) / float64(len(uniqueTokens))
			if noiseRatio > 0.6 {
				score -= int(noiseRatio * 50)
			}
		}
	}

	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}

	return Result{Score: score, TopicMatched: topicMatched}
}
