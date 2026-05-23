package script

import (
	"strings"

	"velox/go-master/internal/media/association"
)

// renderSegmentAssets is intentionally disabled.
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

func renderSegmentPrimaryAssociation(seg TimelineSegment) string {
	label, link := selectPrimaryAssociation(seg)
	if link == "" {
		return ""
	}

	return "Primary Association: " + label + " " + link + "\n"
}

func selectPrimaryAssociation(seg TimelineSegment) (string, string) {
	if link := primaryStockLink(seg); link != "" {
		return "📦 Stock Drive Association", link
	}
	if label, link := primaryArtlistLink(seg); link != "" {
		return label, link
	}
	return "", ""
}

func primaryStockLink(seg TimelineSegment) string {
	if link := firstRenderableLink(seg.PreferredStockPaths); link != "" {
		return link
	}
	for _, match := range seg.StockMatches {
		if link := primaryMatchLink(match); link != "" {
			return link
		}
	}
	return ""
}

func primaryArtlistLink(seg TimelineSegment) (string, string) {
	if strings.Contains(strings.ToLower(strings.TrimSpace(seg.PreferredStockGroup)), "artlist") {
		if link := firstRenderableLink(seg.PreferredStockPaths); link != "" {
			label := "📦 Artlist Drive Association"
			if strings.Contains(strings.ToLower(strings.TrimSpace(seg.PreferredStockGroup)), "live") {
				label = "🚀 Live Artlist Discovery"
			}
			return label, link
		}
	}
	for _, match := range seg.ArtlistMatches {
		if link := primaryMatchLink(match); link != "" {
			label := "📦 Artlist Drive Association"
			source := strings.ToLower(strings.TrimSpace(match.Source))
			if strings.Contains(source, "live") {
				label = "🚀 Live Artlist Discovery"
			}
			return label, link
		}
	}
	return "", ""
}

func firstRenderableLink(values []string) string {
	for _, value := range values {
		if link := normalizeRenderableAssociationLink(value); link != "" {
			return link
		}
	}
	return ""
}

func primaryMatchLink(match association.ScoredMatch) string {
	if link := normalizeRenderableAssociationLink(match.FolderLink); link != "" {
		return link
	}
	if link := normalizeRenderableAssociationLink(match.Link); link != "" {
		return link
	}
	return ""
}

func normalizeRenderableAssociationLink(link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}
	lower := strings.ToLower(link)
	if strings.Contains(lower, "docs.google.com/document/") {
		return ""
	}
	if strings.HasPrefix(lower, "http") {
		return link
	}
	return ""
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
