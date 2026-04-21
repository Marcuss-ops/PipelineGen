package scriptdocs

import (
	"strings"
	"unicode"

	"velox/go-master/internal/stockdb"
)

func clipSearchKeys(clip stockdb.StockClipEntry) []string {
	seen := make(map[string]bool)
	var keys []string

	add := func(raw string) {
		token := strings.TrimSpace(strings.ToLower(raw))
		if len(token) < 3 || seen[token] {
			return
		}
		seen[token] = true
		keys = append(keys, token)
	}

	for _, tag := range clip.Tags {
		for _, part := range strings.FieldsFunc(tag, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}) {
			add(part)
		}
	}

	for _, part := range strings.FieldsFunc(clip.Filename, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		add(part)
	}

	return keys
}

func extractDriveFileIDFromURL(raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return ""
	}
	if i := strings.Index(u, "/file/d/"); i >= 0 {
		rest := u[i+len("/file/d/"):]
		if j := strings.Index(rest, "/"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
		return strings.TrimSpace(rest)
	}
	if i := strings.Index(u, "id="); i >= 0 {
		rest := u[i+len("id="):]
		if j := strings.Index(rest, "&"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
		return strings.TrimSpace(rest)
	}
	return ""
}
