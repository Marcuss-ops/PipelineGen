package matching

import "velox/go-master/pkg/textutil"

// Tokenize splits text into tokens using unicode-aware word boundaries.
// Delegated to pkg/textutil.
func Tokenize(text string) []string {
	return textutil.Tokenize(text)
}

// Normalize cleans and normalizes text for matching.
// Delegated to pkg/textutil.
func Normalize(text string) string {
	return textutil.Normalize(text)
}

// IsStopWord checks if a term is a common stop word.
// Delegated to pkg/textutil.
func IsStopWord(term string) bool {
	return textutil.IsStopWord(term)
}

// TokenizeWithStopWords removes stop words from tokenization.
// Delegated to pkg/textutil.
func TokenizeWithStopWords(text string) []string {
	return textutil.TokenizeWithStopWords(text)
}
