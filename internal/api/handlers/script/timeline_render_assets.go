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

	// 1. Group matches by folder to avoid redundancy
	type FolderGroup struct {
		Name  string
		Link  string
		Clips []string
	}

	folderGroups := make(map[string]*FolderGroup)
	var independentClips []association.ScoredMatch
	seenLinks := make(map[string]bool)

	for _, m := range matches {
		if m.Score < 35 || (m.Link != "" && seenLinks[m.Link]) {
			continue
		}
		if m.Link != "" {
			seenLinks[m.Link] = true
		}

		if m.FolderLink != "" {
			if group, ok := folderGroups[m.FolderLink]; ok {
				group.Clips = append(group.Clips, m.Title)
			} else {
				name := m.FolderName
				if name == "" {
					name = "Related Assets"
				}
				folderGroups[m.FolderLink] = &FolderGroup{
					Name:  name,
					Link:  m.FolderLink,
					Clips: []string{m.Title},
				}
			}
		} else {
			independentClips = append(independentClips, m)
		}
	}

	var b strings.Builder
	b.WriteString("\n   ")
	b.WriteString(label)
	b.WriteString(":\n")

	// 2. Render Live Folders (High Emphasis)
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
		}
	}

	// 3. Render Grouped Folders
	if len(folderGroups) > 0 {
		b.WriteString("      📁 Recommended Folders:\n")
		for _, group := range folderGroups {
			b.WriteString("        - ")
			b.WriteString(group.Name)
			b.WriteString(": ")
			b.WriteString(group.Link)
			b.WriteString("\n")
			// Show up to 2 clip names as examples
			for i, clipName := range group.Clips {
				if i >= 2 {
					break
				}
				b.WriteString("          • ")
				b.WriteString(clipName)
				b.WriteString("\n")
			}
		}
	}

	// 4. Render Independent Clips (if any)
	if len(independentClips) > 0 {
		b.WriteString("      🎬 Individual Clips:\n")
		for i, m := range independentClips {
			if i >= 3 {
				break
			}
			title := m.Title
			if title == "" {
				title = "Asset"
			}
			b.WriteString("        - ")
			b.WriteString(title)
			b.WriteString("\n")
			if m.Link != "" {
				b.WriteString("          Link: ")
				b.WriteString(m.Link)
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}
