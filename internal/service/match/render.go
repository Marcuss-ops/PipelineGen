package match

import (
	"fmt"
	"sort"
	"strings"

	"velox/go-master/internal/service/association"
)

// RenderMatches formatta i match per la visualizzazione nel documento.
func RenderMatches(matches []association.ScoredMatch) string {
	if len(matches) == 0 {
		return "Nessun asset trovato."
	}

	// Sort by score
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].Score > matches[j].Score
	})

	var b strings.Builder
	for i, match := range matches {
		if i > 0 {
			b.WriteString("\n")
		}
		headline := match.Title
		if strings.TrimSpace(match.Path) != "" {
			headline = match.Path
		}
		b.WriteString("- ")
		b.WriteString(headline)
		b.WriteString("\n")

		b.WriteString("  Source: ")
		b.WriteString(match.Source)
		b.WriteString("\n")

		b.WriteString(fmt.Sprintf("  Score: %d\n", match.Score))

		if strings.TrimSpace(match.Link) != "" {
			b.WriteString("  Link: ")
			b.WriteString(match.Link)
			b.WriteString("\n")
		}

		if strings.TrimSpace(match.Details) != "" {
			b.WriteString("  Details: ")
			b.WriteString(match.Details)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}
