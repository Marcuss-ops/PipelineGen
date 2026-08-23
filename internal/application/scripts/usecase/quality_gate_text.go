// Package usecase — quality_gate_text.go
//
// Text-level helpers of the editorial quality gate: placeholder
// detection, tokenisation, stop-word filtering and source-text
// coverage computation.
package usecase

import (
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// isGenericText returns true when the generated text looks like a
// placeholder or generic fallback.
func isGenericText(text string) bool {
	text = strings.ToLower(text)
	placeholders := []string{
		"lorem ipsum",
		"sample text",
		"placeholder",
		"todo:",
		"tbd",
		"insert text here",
		"your text here",
		"generated text",
	}
	for _, p := range placeholders {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

// tokenize returns a slice of lowercased word tokens.
func tokenize(text string) []string {
	var tokens []string
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		word = strings.ToLower(strings.TrimSpace(word))
		if word != "" {
			tokens = append(tokens, word)
		}
	}
	return tokens
}

// computeSourceTextCoverage returns the ratio of generated tokens that
// appear in the source text. Stop words are removed before comparison.
func computeSourceTextCoverage(generated, source string) float64 {
	genTokens := filterStopWords(tokenize(generated))
	if len(genTokens) == 0 {
		return 0.0
	}
	sourceSet := make(map[string]struct{}, len(source))
	for _, t := range filterStopWords(tokenize(source)) {
		sourceSet[t] = struct{}{}
	}
	if len(sourceSet) == 0 {
		return 0.0
	}
	matches := 0
	for _, t := range genTokens {
		if _, ok := sourceSet[t]; ok {
			matches++
		}
	}
	return float64(matches) / float64(len(genTokens))
}

// filterStopWords removes common stop words from a token list.
func filterStopWords(tokens []string) []string {
	stopWords := map[string]struct{}{}
	if registry := linguistics.DefaultLexiconOrNil(); registry != nil {
		stopWords = registry.StopWords("fallback")
	}
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if _, ok := stopWords[t]; !ok {
			out = append(out, t)
		}
	}
	return out
}
