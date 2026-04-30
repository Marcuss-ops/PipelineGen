// Package textutil provides common text processing utilities used across the codebase.
package textutil

import (
	"strings"
	"unicode"
)

// Tokenize splits text into tokens using unicode-aware word boundaries.
// Delegates to internal/matching for consistency.
func Tokenize(text string) []string {
	text = strings.ToLower(text)
	return strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// Normalize cleans and normalizes text for matching.
// Replaces underscores, hyphens, dots with spaces and trims.
func Normalize(text string) string {
	t := strings.ToLower(text)
	t = strings.ReplaceAll(t, "_", " ")
	t = strings.ReplaceAll(t, "-", " ")
	t = strings.ReplaceAll(t, ".", " ")
	return strings.TrimSpace(t)
}

// IsStopWord checks if a term is a common stop word (English and Italian).
func IsStopWord(term string) bool {
	switch term {
	case "the", "and", "for", "with", "that", "this", "from", "then", "into", "over",
		"una", "uno", "del", "della", "delle", "degli", "nel", "nella", "nei",
		"per", "con", "tra", "gli", "le", "dei", "dai", "dalle", "dagli", "sul", "sulla", "sugli":
		return true
	}
	return false
}

// TokenizeWithStopWords removes stop words from tokenization.
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

// DedupeStrings returns a deduplicated copy of the input slice (case-sensitive).
func DedupeStrings(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, s := range input {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}

// DedupeStringsCI returns a deduplicated copy of the input slice (case-insensitive).
func DedupeStringsCI(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, s := range input {
		key := strings.ToLower(s)
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}

// SanitizeFilename removes potentially dangerous characters from a filename.
func SanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "..", "")
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")
	name = strings.ReplaceAll(name, "\x00", "")

	// Keep only safe characters
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-' || c == ' ') {
			name = name[:i] + name[i+1:]
			i--
		}
	}

	name = strings.TrimSpace(name)
	if len(name) > 255 {
		name = name[:255]
	}
	if name == "" {
		name = "unnamed"
	}
	return name
}

// NormalizeQuery normalizes a search query by lowercasing, trimming, and removing spaces/hyphens.
// Used for compact folder/search matching.
func NormalizeQuery(q string) string {
	q = strings.ToLower(strings.TrimSpace(q))
	q = strings.ReplaceAll(q, " ", "")
	q = strings.ReplaceAll(q, "-", "")
	return q
}

// NormalizeStringSlice normalizes a slice of strings (trim, lowercase, filter empty).
func NormalizeStringSlice(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		tag = strings.ToLower(tag)
		out = append(out, tag)
	}
	return out
}

// AlphanumOnly keeps only alphanumeric characters in a string.
// Used for clip key normalization.
func AlphanumOnly(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SimpleNormalize lowercases and trims a string (no character replacement).
// Used for basic match text normalization.
func SimpleNormalize(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}
