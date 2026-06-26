package termutil

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

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
