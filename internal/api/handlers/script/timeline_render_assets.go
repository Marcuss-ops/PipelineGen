package script

import (
	"strings"

	"velox/go-master/internal/service/association"
)

func hasStrongMatch(matches []association.ScoredMatch, minScore int) bool {
	for _, match := range matches {
		if match.Score >= minScore {
			return true
		}
	}
	return false
}

func renderSpecificMatch(label string, matches []association.ScoredMatch) string {
	if len(matches) == 0 {
		return ""
	}
	best := matches[0]
	for _, m := range matches {
		if m.Score > best.Score {
			best = m
		}
	}

	var b strings.Builder
	b.WriteString("\n   ")
	b.WriteString(label)
	b.WriteString(":\n")

	title := best.Title
	if title == "" {
		title = "Asset"
	}
	b.WriteString("      - ")
	b.WriteString(title)
	b.WriteString("\n")

	if best.Link != "" {
		b.WriteString("        Link: ")
		b.WriteString(best.Link)
		b.WriteString("\n")
	} else if best.Path != "" {
		b.WriteString("        Path: ")
		b.WriteString(best.Path)
		b.WriteString("\n")
	}

	return b.String()
}
