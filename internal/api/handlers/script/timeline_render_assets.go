package script

import (
	"strings"

	"velox/go-master/internal/media/association"
)

// renderSegmentAssets renders all asset associations for a segment
func renderSegmentAssets(seg TimelineSegment) string {
	return ""
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
	seenLinks := make(map[string]bool)
	var clippedMatches []association.ScoredMatch
	for _, m := range matches {
		displayLink := resolveTimelineDisplayLink(m)
		if m.Score < 35 || displayLink == "" || seenLinks[displayLink] {
			continue
		}
		seenLinks[displayLink] = true
		clippedMatches = append(clippedMatches, m)
	}

	var b strings.Builder
	b.WriteString("\n   ")
	b.WriteString(label)
	b.WriteString(":\n")

	if len(clippedMatches) > 0 {
		b.WriteString("      🎬 Individual Clips:\n")
		for i, m := range clippedMatches {
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
		if !isDirectArtlistURL(link) && !isDriveFolderURL(link) {
			return link
		}
	}

	return ""
}

func isDirectArtlistURL(link string) bool {
	link = strings.ToLower(strings.TrimSpace(link))
	return strings.Contains(link, "cms-public-artifacts.artlist.io") ||
		strings.Contains(link, "artlist.io")
}

func isDriveFolderURL(link string) bool {
	link = strings.ToLower(strings.TrimSpace(link))
	return strings.Contains(link, "drive.google.com/drive/folders/")
}
