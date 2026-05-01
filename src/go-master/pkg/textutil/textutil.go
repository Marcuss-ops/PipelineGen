// Package textutil provides common text processing utilities used across the codebase.
package textutil

import (
	"strings"
	"unicode"
)

// Tokenize splits text into tokens using unicode-aware word boundaries.
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

// StripCodeFence removes Markdown code fences (e.g. ```json ... ```).
func StripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) >= 2 && strings.HasPrefix(lines[0], "```") && strings.HasSuffix(lines[len(lines)-1], "```") {
			return strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	return s
}

// ExtractJSONObject attempts to find and extract the first JSON object from a string.
func ExtractJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end == -1 || end < start {
		return s
	}
	return s[start : end+1]
}

// ExtractJSONArray attempts to find and extract the first JSON array from a string.
func ExtractJSONArray(s string) string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start == -1 || end == -1 || end < start {
		return s
	}
	return s[start : end+1]
}

// Truncate returns a truncated string with '...' if it exceeds length n.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// SplitCSV splits a comma-separated string into a trimmed slice.
func SplitCSV(text string) []string {
	if text == "" {
		return nil
	}
	parts := strings.Split(text, ",")
	var result []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}

// FirstNonEmpty returns the first non-empty string among the arguments.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// FirstNonEmptySlice returns the first non-empty slice among the arguments.
func FirstNonEmptySlice(primary, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}


// ExtractSentences splits a text block into sentences by splitting on periods.
func ExtractSentences(text string) []string {
	if text == "" {
		return nil
	}
	parts := strings.Split(text, ".")
	var res []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			res = append(res, p)
		}
	}
	return res
}

