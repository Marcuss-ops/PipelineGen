package script

import (
	"strings"

	"velox/go-master/internal/media/association"
)

// renderSegmentAssets renders all asset associations for a segment
func renderSegmentAssets(seg TimelineSegment) string {
	var b strings.Builder
	renderedAny := false

	// Priority 1: Stock Drive Association
	if len(seg.StockMatches) > 0 && hasStrongMatch(seg.StockMatches, 35) {
		label := "📦 Stock Drive Association"
		if !hasStrongMatch(seg.StockMatches, 50) {
			label = "⚠️ Weak Stock Association"
		}
		b.WriteString(renderSpecificMatch(label, seg.StockMatches))
		renderedAny = true
	}

	// Priority 2: Artlist Drive Association
	if len(seg.ArtlistMatches) > 0 && hasStrongMatch(seg.ArtlistMatches, 35) {
		label := "📦 Artlist Drive Association"

		// Check if it was a live discovery
		isLive := false
		for _, m := range seg.ArtlistMatches {
			if m.Source == "artlist_live_discovery" {
				isLive = true
				break
			}
		}

		if isLive {
			label = "🚀 Live Artlist Discovery"
		} else if !hasStrongMatch(seg.ArtlistMatches, 50) {
			label = "⚠️ Weak Artlist Association"
		}
		b.WriteString(renderSpecificMatch(label, seg.ArtlistMatches))
		renderedAny = true
	}

	if !renderedAny {
		b.WriteString("\n   ⚠️ No Association Found\n")
	}

	return b.String()
}

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
		displayLink := resolveTimelineDisplayLink(m)
		if m.Score < 35 || (displayLink != "" && seenLinks[displayLink]) {
			continue
		}

		if displayLink != "" {
			seenLinks[displayLink] = true
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
			if link := resolveTimelineDisplayLink(m); link != "" {
				b.WriteString("          Link: ")
				b.WriteString(link)
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

func resolveTimelineDisplayLink(match association.ScoredMatch) string {
	if link := strings.TrimSpace(match.Link); link != "" {
		if !isDirectArtlistURL(link) {
			return link
		}
	}
	if folderLink := strings.TrimSpace(match.FolderLink); folderLink != "" {
		return folderLink
	}

	return ""
}

func isDirectArtlistURL(link string) bool {
	link = strings.ToLower(strings.TrimSpace(link))
	return strings.Contains(link, "cms-public-artifacts.artlist.io") ||
		strings.Contains(link, "artlist.io")
}
