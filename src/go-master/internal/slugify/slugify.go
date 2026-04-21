package slugify

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var (
	regexpNonAlpha = regexp.MustCompile(`[^a-z0-9-]+`)
	regexpHyphens  = regexp.MustCompile(`-+`)
)

// Marshal trasforma una stringa in uno slug canonico: lowercase, no accenti, hyphen-separated.
func Marshal(s string) string {
	// 1. Normalizza caratteri (accenti -> base)
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(t, s)

	// 2. Lowercase e pulizia
	result = strings.ToLower(result)
	result = regexpNonAlpha.ReplaceAllString(result, "-")
	result = regexpHyphens.ReplaceAllString(result, "-")
	return strings.Trim(result, "-")
}
