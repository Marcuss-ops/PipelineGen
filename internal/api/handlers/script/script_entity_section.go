package script

import (
	"fmt"
	"strings"
)

// renderSpecialNamesWithWiki formats names with 🏷 prefix, optional image links and Wikipedia links
func renderSpecialNamesWithWiki(names []string, images map[string]string, wikis map[string]string) string {
	if len(names) == 0 {
		return "Nessun nome speciale rilevato."
	}
	var b strings.Builder
	for _, n := range names {
		b.WriteString("    🏷 ")
		b.WriteString(n)
		img := strings.TrimSpace(images[n])
		if img != "" {
			b.WriteString(fmt.Sprintf(": %s", img))
		}
		wiki := strings.TrimSpace(wikis[n])
		if wiki != "" {
			b.WriteString(fmt.Sprintf(" (Wikipedia: %s)", wiki))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
