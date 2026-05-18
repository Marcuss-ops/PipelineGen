package termutil

import (
	"strings"
	"velox/go-master/internal/pkg/textutil"
)

// SubjectMatchesTopic checks if a subject matches any of the topic tokens.
func SubjectMatchesTopic(subject string, topicTokens []string) bool {
	if len(topicTokens) == 0 {
		return true
	}
	subjectTokens := TopicTokens(subject)
	if len(subjectTokens) == 0 {
		return false
	}
	for _, tok := range subjectTokens {
		for _, tt := range topicTokens {
			if strings.EqualFold(tok, tt) {
				return true
			}
		}
	}
	return false
}

// TopicTokens extracts tokens from text using the standard tokenizer.
func TopicTokens(text string) []string {
	tokens := textutil.Tokenize(text)
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// ConciseSubject creates a concise subject from text (max 5 words).
func ConciseSubject(text string) string {
	tokens := TopicTokens(text)
	if len(tokens) == 0 {
		return ""
	}
	if len(tokens) > 5 {
		tokens = tokens[:5]
	}
	return strings.Join(tokens, " ")
}

// PreferredEntitySubject finds the best entity/subject match for a topic.
func PreferredEntitySubject(entities []string, subject string, topicTokens []string) string {
	candidates := make([]string, 0, len(entities)+1)
	candidates = append(candidates, entities...)
	candidates = append(candidates, subject)

	best := ""
	bestScore := 0
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		score := 0
		if SubjectMatchesTopic(candidate, topicTokens) {
			score += 50
		}
		if LooksLikePersonName(candidate) {
			score += 20
		}
		words := strings.Fields(candidate)
		if len(words) >= 2 && len(words) <= 4 {
			score += 15
		}
		if score > bestScore {
			bestScore = score
			best = candidate
		}
	}
	if bestScore < 50 {
		return ""
	}
	return best
}
