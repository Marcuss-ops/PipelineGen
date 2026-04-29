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

// IsStopWord checks if a term is a common stop word
func IsStopWord(term string) bool {
	switch term {
	case "the", "and", "for", "with", "that", "this", "from", "then", "into", "over",
		"una", "uno", "del", "della", "delle", "degli", "nel", "nella", "nei",
		"per", "con", "tra", "gli", "le", "dei", "dai", "dalle", "dagli", "sul", "sulla", "sugli":
		return true
	}
	return false
}

// TokenizeWithStopWords removes stop words from tokenization
func TokenizeWithStopWords(text string) []string {
	tokens := Tokenize(text)
	result := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if len(tok) >= 3 && !IsStopWord(tok) {
			result = append(result, tok)
		}
	}
	return result
}
