package script

import (
	"strings"
	"unicode"
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
	s = strings.TrimSpace(s)
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start != -1 && end != -1 && end > start {
		return s[start : end+1]
	}
	return s
}

func splitCSV(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	parts := strings.Split(text, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
