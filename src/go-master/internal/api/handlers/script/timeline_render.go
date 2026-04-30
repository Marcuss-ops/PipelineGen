package script

import (
	"fmt"
	"sort"
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
			// truncateString is defined in script_docs_generation_helpers.go
			b.WriteString(truncateString(seg.OpeningSentence, 80))
			b.WriteString("\n")
		}

		// Show ONLY ONE primary asset source for clarity
		stockContent := renderTimelineAssetMatches("📦 DRIVE STOCK", seg.StockMatches)
		artlistDriveContent := renderArtlistDriveMatches(seg)

		if stockContent != "" {
			b.WriteString(stockContent)
		} else if artlistDriveContent != "" {
			b.WriteString(artlistDriveContent)
		}

		// Artlist Phrases - Strict global limit of 6
		remainingBudget := maxGlobalArtlist - globalArtlistCount
		if remainingBudget > 0 {
			artlistContent, count := renderPhrasesWithLimit(seg, remainingBudget)
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

func renderPhrasesWithLimit(seg TimelineSegment, budget int) (string, int) {
	if budget <= 0 {
		return "", 0
	}
	var b strings.Builder
	
	phrases := getPhraseCandidates(seg, 2)
	if len(phrases) == 0 {
		return "", 0
	}
	if budget < len(phrases) {
		phrases = phrases[:budget]
	}

	var allMatches []scoredMatch
	allMatches = append(allMatches, seg.ArtlistMatches...)
	allMatches = append(allMatches, seg.DriveMatches...)

	count := 0
	displayLimit := len(phrases)
	
	b.WriteString("\n   🎵 ARTLIST PHRASES:\n")
	for i := 0; i < displayLimit; i++ {
		phrase := phrases[i]
		b.WriteString("      - \"")
		b.WriteString(phrase)
		b.WriteString("\"\n")
		
		if i < len(allMatches) {
			match := allMatches[i]
			title := match.Title
			if title == "" { title = "Asset" }
			b.WriteString("        -> ")
			b.WriteString(title)
			b.WriteString("\n")
			if match.Link != "" {
				b.WriteString("           Link: ")
				b.WriteString(match.Link)
				b.WriteString("\n")
			}
		} else {
			b.WriteString("        -> Search suggestion\n")
		}
		count++
	}
	return b.String(), count
}

func renderArtlistDriveMatches(seg TimelineSegment) string {
	if len(seg.ArtlistMatches) == 0 {
		return ""
	}
	var best scoredMatch
	for _, m := range seg.ArtlistMatches {
		if m.Link != "" {
			best = m
			break
		}
	}
	if best.Link == "" && len(seg.ArtlistMatches) > 0 {
		best = seg.ArtlistMatches[0]
	}
	if best.Title == "" && best.Link == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n   📦 ARTLIST DRIVE:\n")
	title := best.Title
	if title == "" { title = "Artlist Clip" }
	b.WriteString("      - ")
	b.WriteString(title)
	b.WriteString("\n")
	if best.Link != "" {
		b.WriteString("         Link: ")
		b.WriteString(best.Link)
		b.WriteString("\n")
	}
	return b.String()
}

func renderTimelineAssetMatches(label string, matches []scoredMatch) string {
	if len(matches) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n   ")
	b.WriteString(label)
	b.WriteString(":\n")

	for _, match := range matches {
		title := strings.Trim(match.Title, "\"'“‘’”")
		if title == "" { title = "Stock Folder" }
		b.WriteString("      - " + title + "\n")
		if match.Link != "" {
			b.WriteString("         Link: " + match.Link + "\n")
		}
	}
	return b.String()
}

func getPhraseCandidates(seg TimelineSegment, limit int) []string {
	sentences := extractNarrativeSentences(seg.NarrativeText)
	if len(sentences) == 0 { return nil }
	
	sort.Slice(sentences, func(i, j int) bool {
		return len(sentences[i]) > len(sentences[j])
	})
	
	if len(sentences) > limit {
		sentences = sentences[:limit]
	}
	return sentences
}
