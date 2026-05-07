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

	globalArtlistCount := 0
	const maxGlobalArtlist = 6
	const minAssetScore = 25 // Minimum score to accept a local asset match

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

		// 1. ASSET ASSOCIATIONS
		assetRendered := false

		// Priority 1: Stock Drive Association (Cartelle stock locali)
		if len(seg.StockMatches) > 0 && hasStrongMatch(seg.StockMatches, minAssetScore) {
			b.WriteString(renderSpecificMatch("📦 Stock Drive Association", seg.StockMatches))
			assetRendered = true
		}

		// Priority 2: Artlist Drive Association (Database Artlist sincronizzato)
		if !assetRendered && len(seg.ArtlistMatches) > 0 && hasStrongMatch(seg.ArtlistMatches, minAssetScore) {
			b.WriteString(renderSpecificMatch("📦 Artlist Drive Association", seg.ArtlistMatches))
			assetRendered = true
		}

		// Priority 3: Dynamic Artlist Association (Suggerimenti LLM per download)
		if !assetRendered && len(seg.SearchSuggestions) > 0 {
			b.WriteString("\n   🔍 Dynamic Artlist Association:\n")
			for _, kw := range seg.SearchSuggestions {
				// Clean and format keywords (fuzzy-friendly)
				kw = strings.TrimSpace(kw)
				if kw == "" {
					continue
				}
				b.WriteString(fmt.Sprintf("      - \"%s\"\n", kw))
				searchURL := "https://artlist.io/stock-video/s/" + strings.ReplaceAll(strings.ToLower(kw), " ", "-")
				b.WriteString("        Link: ")
				b.WriteString(searchURL)
				b.WriteString("\n")
				b.WriteString("        -> Search suggestion (Pending download)\n")
			}
			assetRendered = true
		} else if !assetRendered {
			b.WriteString("\n   ⚠️ No Association Found\n")
		}

		// 2. VISUAL SEARCH QUERIES (from LLM VisualQueryResult)
		if len(seg.SearchSuggestions) > 0 {
			b.WriteString("\n   🔍 Visual Search Queries (LLM-generated):\n")
			for _, query := range seg.SearchSuggestions {
				b.WriteString(fmt.Sprintf("      - %s\n", query))
				searchURL := "https://artlist.io/stock-video/s/" + strings.ReplaceAll(strings.ToLower(query), " ", "-")
				b.WriteString("        Link: ")
				b.WriteString(searchURL)
				b.WriteString("\n")
			}
		}

	// 3. ARTLIST PHRASES (now using SearchSuggestions from LLM)
		remainingBudget := maxGlobalArtlist - globalArtlistCount
		if remainingBudget > 0 && len(seg.SearchSuggestions) > 0 {
			b.WriteString("\n   🎵 ARTLIST PHRASES (visual queries):\n")
			for i, query := range seg.SearchSuggestions {
				if i >= remainingBudget {
					break
				}
				preview := seg.NarrativeText
				if len(preview) > 50 {
					preview = preview[:50]
				}
				b.WriteString(fmt.Sprintf("      - \"%s\"\n", preview))
				b.WriteString(fmt.Sprintf("        Query: %s\n", query))
				searchURL := "https://artlist.io/stock-video/s/" + strings.ReplaceAll(strings.ToLower(query), " ", "-")
				b.WriteString("        Link: ")
				b.WriteString(searchURL)
				b.WriteString("\n")
		}
	}

		if seg.Index < len(plan.Segments) {
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func hasRenderableStockMatch(matches []association.ScoredMatch) bool {
	for _, match := range matches {
		if strings.TrimSpace(match.Link) != "" || strings.TrimSpace(match.Path) != "" {
			return true
		}
	}
	return false
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
		displayLabel = "📦 Artlist Drive Association"
	case string(timelineAssetSourceArtlistDynamic):
		displayLabel = "🔍 Dynamic Artlist Association"
	case "drive_stock", "stock_folder", "stock_drive":
		displayLabel = "📦 Stock Drive Association"
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

// Deprecated: renderOnlyPhrases is no longer used. Visual queries now come from LLM VisualQueryResult.
func renderOnlyPhrases(seg TimelineSegment, budget int) (string, int) {
	return "", 0
}

// Deprecated: scoreArtlistPhrase is no longer used.
func scoreArtlistPhrase(phrase string) int {
	return 0
}
