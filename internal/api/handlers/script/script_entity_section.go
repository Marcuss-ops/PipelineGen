package script

import (
	"fmt"
	"strings"
	"unicode"
)

// renderSpecialNamesWithWiki formats names with 🏷 prefix, optional image links and Wikipedia links.
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

func filterSpecialNames(names []string, topic string) []string {
	seen := make(map[string]bool, len(names))
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		normalized := strings.TrimSpace(name)
		if !isLikelySpecialName(normalized, topic) {
			continue
		}
		lower := strings.ToLower(normalized)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		filtered = append(filtered, normalized)
	}
	return filtered
}

func isLikelySpecialName(name, topic string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if topic != "" && strings.EqualFold(name, strings.TrimSpace(topic)) {
		return false
	}
	if strings.ContainsAny(name, ",;:!?\"()") {
		return false
	}

	fields := strings.Fields(name)
	if len(fields) == 0 || len(fields) > 3 {
		return false
	}

	starter := strings.ToLower(strings.Trim(fields[0], "'\"()[]{}"))
	sentenceStarters := map[string]bool{
		"hanno":   true,
		"ha":      true,
		"hai":     true,
		"abbiamo": true,
		"avete":   true,
		"sono":    true,
		"era":     true,
		"erano":   true,
		"fu":      true,
		"si":      true,
		"c":       true,
		"che":     true,
		"quando":  true,
		"dove":    true,
		"perche":  true,
	}
	if sentenceStarters[starter] {
		return false
	}

	hasCapital := false
	for _, field := range fields {
		clean := strings.Trim(field, "'\"()[]{}")
		if clean == "" {
			continue
		}
		lower := strings.ToLower(clean)
		if isAllowedNameConnector(lower) {
			continue
		}
		if isLikelyGenericSpecialNameWord(lower) {
			return false
		}
		r := []rune(clean)
		if len(r) > 0 && unicode.IsUpper(r[0]) {
			hasCapital = true
		}
	}

	return hasCapital
}

func isAllowedNameConnector(word string) bool {
	switch word {
	case "da", "de", "di", "del", "della", "dei", "degli", "van", "von", "la", "le", "al", "el", "d'":
		return true
	default:
		return false
	}
}

func isLikelyGenericSpecialNameWord(word string) bool {
	switch word {
	case "old", "vintage", "film", "history", "cinematic", "legacy", "object", "story", "scene", "documentary", "footage", "image", "key":
		return true
	default:
		return false
	}
}
