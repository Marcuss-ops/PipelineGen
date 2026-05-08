package script

import (
	"fmt"
	"strings"
	"velox/go-master/internal/service/association"
	"velox/go-master/pkg/textutil"
)

// RenderTimeline converts a TimelinePlan into the final formatted text section.
func RenderTimeline(plan *TimelinePlan) string {
	if plan == nil || len(plan.Segments) == 0 {
		return "⏱️ Timeline unavailable."
	}

	var b strings.Builder

	for _, seg := range plan.Segments {
		b.WriteString("[")
		b.WriteString(seg.Timestamp)
		b.WriteString("]\n")

		if seg.Subject != "" {
			b.WriteString(fmt.Sprintf("   Subject: %s\n", seg.Subject))
		}

		if strings.TrimSpace(seg.OpeningSentence) != "" {
			b.WriteString("   Start: ")
			b.WriteString(textutil.Truncate(seg.OpeningSentence, 80))
			b.WriteString("\n")
		}
		if strings.TrimSpace(seg.ClosingSentence) != "" {
			b.WriteString("   End:   ")
			b.WriteString(textutil.Truncate(seg.ClosingSentence, 80))
			b.WriteString("\n")
		}

		// ASSET ASSOCIATIONS
		assetRendered := false

		// Priority 1: Stock Drive Association
		if len(seg.StockMatches) > 0 && hasStrongMatch(seg.StockMatches, 25) {
			b.WriteString(renderSpecificMatch("📦 Stock Drive Association", seg.StockMatches))
			assetRendered = true
		}

		// Priority 2: Artlist Drive Association
		if !assetRendered && len(seg.ArtlistMatches) > 0 && hasStrongMatch(seg.ArtlistMatches, 25) {
			b.WriteString(renderSpecificMatch("📦 Artlist Drive Association", seg.ArtlistMatches))
			assetRendered = true
		}

		if !assetRendered {
			b.WriteString("\n   ⚠️ No Association Found\n")
		}

		if seg.Index < len(plan.Segments) {
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
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
