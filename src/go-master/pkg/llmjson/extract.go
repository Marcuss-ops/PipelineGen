package llmjson

import (
	"strings"
)

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

// ExtractObject attempts to find and extract the first JSON object from a string.
func ExtractObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end == -1 || end < start {
		return s
	}
	return s[start : end+1]
}

// ExtractArray attempts to find and extract the first JSON array from a string.
func ExtractArray(s string) string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start == -1 || end == -1 || end < start {
		return s
	}
	return s[start : end+1]
}
