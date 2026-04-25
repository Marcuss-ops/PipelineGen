package script

import "strings"

// renderTimelinePlan renders a timeline plan to a string.
func renderTimelinePlan(plan *TimelinePlan) string {
	if plan == nil || len(plan.Segments) == 0 {
		return "⚠️ Timeline unavailable."
	}

	var b strings.Builder
	for _, seg := range plan.Segments {
		b.WriteString("\n📍 [")
		b.WriteString(seg.Timestamp)
		b.WriteString("]")
		b.WriteString("\n")
		if strings.TrimSpace(seg.OpeningSentence) != "" {
			b.WriteString("   🎬 Inizio: ")
			b.WriteString(seg.OpeningSentence)
			b.WriteString("\n")
		}
		if strings.TrimSpace(seg.ClosingSentence) != "" {
			b.WriteString("   🎬 Fine:   ")
			b.WriteString(seg.ClosingSentence)
			b.WriteString("\n")
		}

		// Always show labels, use "None" if empty
		artlistContent := renderTimelineMatches("🎞️ CLIP ARTLIST", seg.ArtlistMatches)
		if artlistContent == "" {
			b.WriteString("\n   🎞️ CLIP ARTLIST:\n      • None\n")
		} else {
			b.WriteString(artlistContent)
		}

		driveContent := renderTimelineMatches("📦 DRIVE STOCK", seg.DriveMatches)
		if driveContent == "" {
			b.WriteString("\n   📦 DRIVE STOCK:\n      • None\n")
		} else {
			b.WriteString(driveContent)
		}
	}

	return strings.TrimSpace(b.String())
}

func renderTimelineMatches(label string, matches []scoredMatch) string {
	if len(matches) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n   ")
	b.WriteString(label)
	b.WriteString(":\n")

	// Group by phrase/title
	groups := make(map[string][]string)
	var order []string
	for _, match := range matches {
		if _, ok := groups[match.Title]; !ok {
			order = append(order, match.Title)
		}
		// ensure unique links per phrase
		duplicate := false
		for _, existing := range groups[match.Title] {
			if existing == match.Link {
				duplicate = true
				break
			}
		}
		if !duplicate {
			groups[match.Title] = append(groups[match.Title], match.Link)
		}
	}

	for _, title := range order {
		links := groups[title]
		b.WriteString("      🎬 ")
		if title != "" {
			cleanTitle := strings.Trim(title, "\"'“‘’”")
			b.WriteString("\"" + cleanTitle + "\"")
		} else {
			b.WriteString("Clip")
		}
		b.WriteString("\n")

		if len(links) > 0 && links[0] != "" {
			// ONLY ONE LINK per phrase as requested
			b.WriteString("         🚀 " + links[0] + "\n")
		} else {
			b.WriteString("         ⚠️ (Nessun video trovato nel database locale)\n")
		}
	}
	return b.String()
}
