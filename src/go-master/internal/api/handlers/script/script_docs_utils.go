package script

import (
	"math"
	"strings"
	"unicode"
)

func uniqueStrings(in []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range in {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) > 2 {
			return strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	return s
}

func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start != -1 && end != -1 && end > start {
		return s[start : end+1]
	}
	return s
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

func roundSeconds(f float64) float64 {
	return math.Round(f*10) / 10
}

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

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
