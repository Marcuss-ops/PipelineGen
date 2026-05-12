package script

import "strings"

// renderSpecialNamesWithImages formats names with 🏷 prefix
func renderSpecialNamesWithImages(names []string, images map[string]string) string {
	if len(names) == 0 {
		return "Nessun nome speciale rilevato."
	}
	var b strings.Builder
	for _, n := range names {
		b.WriteString("    🏷 ")
		b.WriteString(n)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
