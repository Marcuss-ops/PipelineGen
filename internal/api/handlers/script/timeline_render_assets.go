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

	// Group and filter matches
	var highValue []association.ScoredMatch
	seenLinks := make(map[string]bool)

	for _, m := range matches {
		if m.Score < 35 || (m.Link != "" && seenLinks[m.Link]) {
			continue
		}
		if m.Link != "" {
			seenLinks[m.Link] = true
		}
		highValue = append(highValue, m)
	}

	if len(highValue) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n   ")
	b.WriteString(label)
	b.WriteString(":\n")

	// 1. Render Folders first
	for _, m := range matches {
		if m.Source == "drive_folder_live" {
			b.WriteString("      📂 ")
			b.WriteString(m.Title)
			if m.Link != "" {
				b.WriteString("\n        Link: ")
				b.WriteString(m.Link)
			}
			b.WriteString("\n")
		}
	}

	// 2. Render Clips (up to 2)
	renderedClips := 0
	for _, m := range matches {
		if m.Source == "drive_folder_live" {
			continue
		}
		if m.Score < 35 {
			continue
		}
		
		title := m.Title
		if title == "" {
			title = "Asset"
		}
		b.WriteString("      🎬 ")
		b.WriteString(title)
		b.WriteString("\n")

		if m.Link != "" {
			b.WriteString("        Link: ")
			b.WriteString(m.Link)
			b.WriteString("\n")
		} else if m.Path != "" {
			b.WriteString("        Path: ")
			b.WriteString(m.Path)
			b.WriteString("\n")
		}
		
		renderedClips++
		if renderedClips >= 2 {
			break
		}
	}

	return b.String()
}
