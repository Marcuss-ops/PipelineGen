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

	// 1. Render Folders first (High Emphasis)
	folderFound := false
	for _, m := range matches {
		if m.Source == "drive_folder_live" {
			b.WriteString("      📂 Destination Drive Folder:\n")
			if m.Link != "" {
				b.WriteString("        Link: ")
				b.WriteString(m.Link)
			} else {
				b.WriteString("        Status: Resolving...")
			}
			b.WriteString("\n")
			folderFound = true
		}
	}

	// 2. Render Clips (up to 2 if folder found, up to 3 if not)
	limit := 3
	if folderFound {
		limit = 2
		b.WriteString("      🎬 Assets being uploaded to Drive:\n")
	} else {
		b.WriteString("      🎬 Available Clips:\n")
	}

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
		b.WriteString("        - ")
		b.WriteString(title)
		b.WriteString("\n")

		// ONLY show raw link if we DON'T have a drive folder
		if !folderFound && m.Link != "" {
			b.WriteString("          Link: ")
			b.WriteString(m.Link)
			b.WriteString("\n")
		}
		
		renderedClips++
		if renderedClips >= limit {
			break
		}
	}

	return b.String()
}
