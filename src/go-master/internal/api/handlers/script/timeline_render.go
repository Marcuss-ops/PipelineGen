package script

import (
	"fmt"
	"strings"
)

// RenderTimeline converts a TimelinePlan into the final formatted text section.
func RenderTimeline(plan *TimelinePlan) string {
	if plan == nil || len(plan.Segments) == 0 {
		return "⏱️ Timeline unavailable."
	}

	var b strings.Builder

	globalArtlistCount := 0
	const maxGlobalArtlist = 6

	for _, seg := range plan.Segments {
		b.WriteString("[")
		b.WriteString(seg.Timestamp)
		b.WriteString("]\n")

		if seg.Subject != "" {
			b.WriteString(fmt.Sprintf("   Subject: %s\n", seg.Subject))
		}

		if strings.TrimSpace(seg.OpeningSentence) != "" {
			b.WriteString("   Start: ")
			b.WriteString(truncateString(seg.OpeningSentence, 80))
			b.WriteString("\n")
		}

		// 1. ASSET ASSOCIATIONS
		assetRendered := false

		// Priority 1: Drive Stock Association (Cartelle locali)
		if len(seg.StockMatches) > 0 {
			if hasRenderableStockMatch(seg.StockMatches) {
				b.WriteString(renderSpecificMatch("📦 Drive Stock Association", seg.StockMatches))
				assetRendered = true
			} else if len(seg.ArtlistMatches) > 0 {
				b.WriteString(renderSpecificMatch("📦 Artlist Folder Association", seg.ArtlistMatches))
				assetRendered = true
			} else {
				b.WriteString(renderSpecificMatch("📦 Drive Stock Association", seg.StockMatches))
				assetRendered = true
			}
		}

		// Priority 2: Artlist Stock Association (Database Artlist)
		if !assetRendered && len(seg.ArtlistMatches) > 0 {
			b.WriteString(renderSpecificMatch("📦 Artlist Stock Association", seg.ArtlistMatches))
			assetRendered = true
		}

		// Priority 3: Clip Drive Association (Clip specifiche scaricate)
		if !assetRendered && len(seg.DriveMatches) > 0 {
			b.WriteString(renderSpecificMatch("📦 Clip Drive Association", seg.DriveMatches))
			assetRendered = true
		}

		// Priority 4: Dynamic Artlist Association (Suggerimenti LLM)
		if !assetRendered && len(seg.SearchSuggestions) > 0 {
			if strings.TrimSpace(seg.Timestamp) != "" {
				b.WriteString("\n   [")
				b.WriteString(seg.Timestamp)
				b.WriteString("]\n")
			}
			b.WriteString("\n   🔍 Dynamic Artlist Association:\n")
			for _, kw := range seg.SearchSuggestions {
				b.WriteString(fmt.Sprintf("      - \"%s\"\n", kw))
				b.WriteString("        -> Search suggestion (Pending download)\n")
			}
		} else if !assetRendered {
			if strings.TrimSpace(seg.Timestamp) != "" {
				b.WriteString("\n   [")
				b.WriteString(seg.Timestamp)
				b.WriteString("]\n")
			}
			b.WriteString("\n   ⚠️ No Association Found\n")
		}

		// 2. ARTLIST PHRASES (Separate support section)
		remainingBudget := maxGlobalArtlist - globalArtlistCount
		if remainingBudget > 0 {
			artlistContent, count := renderOnlyPhrases(seg, remainingBudget)
			if artlistContent != "" {
				b.WriteString(artlistContent)
				globalArtlistCount += count
			}
		}

		if seg.Index < len(plan.Segments) {
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func hasRenderableStockMatch(matches []scoredMatch) bool {
	for _, match := range matches {
		if strings.TrimSpace(match.Link) != "" || strings.TrimSpace(match.Path) != "" {
			return true
		}
	}
	return false
}

func renderSpecificMatch(label string, matches []scoredMatch) string {
	if len(matches) == 0 {
		return ""
	}
	// Prendiamo il migliore per score
	best := matches[0]
	for _, m := range matches {
		if m.Score > best.Score {
			best = m
		}
	}

	displayLabel := label
	switch best.Source {
	case string(timelineAssetSourceArtlistFolder):
		displayLabel = "📦 Artlist Folder Association"
	case string(timelineAssetSourceArtlistDynamic):
		displayLabel = "📦 Dynamic Artlist Association"
	case "clip_drive":
		displayLabel = "📦 Clip Drive Association"
	case "drive_stock":
		displayLabel = "📦 Drive Stock Association"
	}

	var b strings.Builder
	b.WriteString("\n   ")
	b.WriteString(displayLabel)
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
	} else {
		b.WriteString("        Path: None\n")
	}

	return b.String()
}

func renderOnlyPhrases(seg TimelineSegment, budget int) (string, int) {
	if budget <= 0 {
		return "", 0
	}

	sentences := extractNarrativeSentences(seg.NarrativeText)
	if len(sentences) == 0 {
		return "", 0
	}

	// Limitiamo a 2 frasi per segmento
	limit := 2
	if limit > budget {
		limit = budget
	}
	if len(sentences) > limit {
		sentences = sentences[:limit]
	}

	var b strings.Builder
	if strings.TrimSpace(seg.Timestamp) != "" {
		b.WriteString("\n   [")
		b.WriteString(seg.Timestamp)
		b.WriteString("]\n")
	}
	b.WriteString("\n   🎵 ARTLIST PHRASES:\n")
	for _, phrase := range sentences {
		b.WriteString("      - \"")
		b.WriteString(phrase)
		b.WriteString("\"\n")
	}

	return b.String(), len(sentences)
}
