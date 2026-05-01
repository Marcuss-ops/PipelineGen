package matching

import (
	"strings"
	"unicode"
)

// Tokenize splits text into tokens using unicode-aware word boundaries
func Tokenize(text string) []string {
	text = strings.ToLower(text)
	return strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// Normalize cleans and normalizes text for matching
func Normalize(text string) string {
	t := strings.ToLower(text)
	t = strings.ReplaceAll(t, "_", " ")
	t = strings.ReplaceAll(t, "-", " ")
	t = strings.ReplaceAll(t, ".", " ")
	return strings.TrimSpace(t)
}

// IsStopWord intentionally returns false here so matching logic can stay free
// of hardcoded stop-word lists. Callers still keep their existing guard rails
// around token length and structural noise.
func IsStopWord(term string) bool {
	_ = term
	return false
}

// TokenizeWithStopWords currently reuses the shared tokenizer without
// introducing any hardcoded stop-word policy in this layer.
func TokenizeWithStopWords(text string) []string {
	return Tokenize(text)
}
