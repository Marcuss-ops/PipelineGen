package script

import (
	"strings"
)

// RenderTimeline converts a TimelinePlan into the final formatted text section.
func RenderTimeline(plan *TimelinePlan) string {
	if plan == nil || len(plan.Segments) == 0 {
		return "⏱️ Timeline unavailable."
	}

	var b strings.Builder

	for _, seg := range plan.Segments {
		b.WriteString(renderSegmentHeader(seg))

		// ASSET ASSOCIATIONS
		assetRendered := false

		// Priority 1: Stock Drive Association
		if len(seg.StockMatches) > 0 && hasStrongMatch(seg.StockMatches, 35) {
			label := "📦 Stock Drive Association"
			if !hasStrongMatch(seg.StockMatches, 50) {
				label = "⚠️ Weak Stock Association"
			}
			b.WriteString(renderSpecificMatch(label, seg.StockMatches))
			assetRendered = true
		}

		// Priority 2: Artlist Drive Association
		if !assetRendered && len(seg.ArtlistMatches) > 0 && hasStrongMatch(seg.ArtlistMatches, 35) {
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
