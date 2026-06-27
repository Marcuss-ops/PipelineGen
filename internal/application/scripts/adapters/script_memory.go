// Package memory — normalize.go provides text normalization helpers
// used by the gemmamemory caching layer.
package adapters

import (
	"strings"
	"unicode"
)

// NormalizeSearchText normalizes text for search/lookup: lowercase,
// keep only alphanumeric characters and spaces.
func NormalizeSearchText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	lastWasSpace := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastWasSpace = false
		} else if r == ' ' && !lastWasSpace {
			b.WriteRune(' ')
			lastWasSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}
