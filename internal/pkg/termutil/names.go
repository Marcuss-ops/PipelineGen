package termutil

import (
	"strings"
	"unicode"
	"velox/go-master/internal/pkg/sliceutil"
)

// LooksLikePersonName checks if the text looks like a person's name.
func LooksLikePersonName(text string) bool {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 || len(parts) > 5 {
		return false
	}
	score := 0
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		first := []rune(part)[0]
		if first >= 'A' && first <= 'Z' {
			score++
		}
	}
	return score >= 1 && len(parts) <= 4
}

// ExtractLikelyNames extracts words that look like names (capitalized, >2 chars).
func ExtractLikelyNames(text string) []string {
	var names []string
	words := strings.Fields(text)
	for _, w := range words {
		w = strings.Trim(w, ".,!?:;\"'()")
		if len(w) > 2 && unicode.IsUpper(rune(w[0])) {
			names = append(names, w)
		}
	}
	return sliceutil.UniqueStrings(names)
}
