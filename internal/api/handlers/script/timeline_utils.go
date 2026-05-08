package script

import (
	"strings"

	"velox/go-master/pkg/textutil"
)

// conciseSubject extracts a short subject from text (DISABLED - produces bad subjects)
func conciseSubject(text string) string {
	tokens := topicTokensFromText(text)
	if len(tokens) == 0 {
		return ""
	}
	if len(tokens) > 5 {
		tokens = tokens[:5]
	}
	return strings.Join(tokens, " ")
}

func topicTokens(topic string) []string {
	return topicTokensFromText(topic)
}

func topicTokensFromText(text string) []string {
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
