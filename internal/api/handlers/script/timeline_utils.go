package script

import (
	"strings"

	"velox/go-master/internal/pkg/textutil"
)

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
