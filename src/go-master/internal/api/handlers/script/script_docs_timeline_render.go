package script

import "strings"

// renderTimelinePlan renders a timeline plan to a string.
func renderTimelinePlan(plan *TimelinePlan) string {
	if plan == nil || len(plan.Segments) == 0 {
		return "⏱️ Timeline unavailable."
	}

	var b strings.Builder
	for _, seg := range plan.Segments {
		b.WriteString("\n[")
		b.WriteString(seg.Timestamp)
		b.WriteString("]")
		b.WriteString("\n")
		if strings.TrimSpace(seg.OpeningSentence) != "" {
			b.WriteString("   Inizio: ")
			b.WriteString(seg.OpeningSentence)
			b.WriteString("\n")
		}
		if strings.TrimSpace(seg.ClosingSentence) != "" {
			b.WriteString("   Fine:   ")
			b.WriteString(seg.ClosingSentence)
			b.WriteString("\n")
		}

		driveContent := renderTimelineMatches("📦 DRIVE STOCK", seg.DriveMatches)
		if driveContent == "" {
			b.WriteString("\n   📦 DRIVE STOCK:\n      - None\n")
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

	for _, match := range matches {
		title := strings.Trim(match.Title, "\"'“‘’”")
		if title == "" {
			title = "Stock Folder"
		}
		
		b.WriteString("      - " + title + "\n")

		if match.Link != "" {
			b.WriteString("         " + match.Link + "\n")
		} else if strings.TrimSpace(match.Details) != "" {
			b.WriteString("         Tag suggeriti: " + match.Details + "\n")
			b.WriteString("         (Nessun video trovato nel database locale)\n")
		} else {
			b.WriteString("         (Nessun video trovato nel database locale)\n")
		}
	}
	return b.String()
}
