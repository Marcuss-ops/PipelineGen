package script

import (
	"strings"
	"unicode"
	"velox/go-master/pkg/textutil"
)

func extractLikelyNames(text string) []string {
	var names []string
	words := strings.Fields(text)
	for _, w := range words {
		w = strings.Trim(w, ".,!?:;\"'()")
		if len(w) > 2 && unicode.IsUpper(rune(w[0])) {
			names = append(names, w)
		}
	}
	return uniqueStrings(names)
}

func extractJSONArray(s string) string {
	return textutil.ExtractJSONArray(s)
}

func splitCSV(text string) []string {
	return textutil.SplitCSV(text)
}
