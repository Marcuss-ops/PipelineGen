package script

import "strings"

// renderTimelinePlan renders a timeline plan to a string.
func renderTimelinePlan(plan *TimelinePlan) string {
	if plan == nil || len(plan.Segments) == 0 {
		return "Timeline unavailable."
	}

	var b strings.Builder
	for _, seg := range plan.Segments {
		b.WriteString("\n[")
		b.WriteString(seg.Timestamp)
		b.WriteString("]")
		b.WriteString("\n")
		if strings.TrimSpace(seg.OpeningSentence) != "" {
			b.WriteString("  Opening: ")
			b.WriteString(seg.OpeningSentence)
			b.WriteString("\n")
		}
		if strings.TrimSpace(seg.ClosingSentence) != "" {
			b.WriteString("  Closing: ")
			b.WriteString(seg.ClosingSentence)
			b.WriteString("\n")
		}

		writeMatches := func(label string, matches []scoredMatch) {
			b.WriteString("  ")
			b.WriteString(label)
			b.WriteString(" links:\n")
			if len(matches) == 0 {
				b.WriteString("    None\n")
				return
			}
			for _, match := range matches {
				b.WriteString("    - ")
				if strings.TrimSpace(match.Path) != "" {
					b.WriteString(match.Path)
				} else {
					b.WriteString(match.Title)
				}
				b.WriteString("\n")
				if strings.TrimSpace(match.Link) != "" {
					if label == "Stock" {
						b.WriteString("      Folder: ")
					} else {
						b.WriteString("      Link: ")
					}
					b.WriteString(match.Link)
					b.WriteString("\n")
				}
			}
		}

		writeMatches("Stock", seg.StockMatches)
		writeMatches("Drive", seg.DriveMatches)
		writeMatches("Artlist", seg.ArtlistMatches)
	}

	return strings.TrimSpace(b.String())
}
