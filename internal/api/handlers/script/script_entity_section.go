package script

import (
	"strings"
	"unicode"
)

// extractSpecialNames finds proper nouns (start with uppercase) in narrative
// Filters out sentence-start words that are common words, not names
// DISABLED: Fragile uppercase heuristic produces false positives
// TODO: Replace with LLM-based entity extraction using BuildEntityExtractionPrompt
func extractSpecialNames(narrative string) []string {
	// Common sentence starters that are not proper nouns
	sentenceStarters := map[string]struct{}{
		"the": {}, "she's": {}, "he": {}, "her": {}, "his": {},
		"this": {}, "that": {}, "these": {}, "those": {},
		"it": {}, "its": {}, "today": {}, "tomorrow": {},
		"yesterday": {}, "now": {}, "then": {}, "soon": {},
	}
	var names []string
	seen := make(map[string]struct{})
	words := strings.Fields(narrative)

	for i, w := range words {
		if len(w) == 0 {
			continue
		}
		firstChar := []rune(w)[0]
		if !unicode.IsUpper(firstChar) {
			continue
		}
		// Clean punctuation
		cleanWord := strings.TrimRight(w, ",.!?;:\"'")
		if cleanWord == "" {
			continue
		}
		// Skip if it's just a sentence starter (and at sentence start position)
		key := strings.ToLower(cleanWord)
		if _, isStarter := sentenceStarters[key]; isStarter {
			// Check if this is likely a sentence start (previous word ends with .!? or it's first word)
			if i == 0 || isEndOfSentence(words, i-1) {
				continue
			}
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, cleanWord)
		if len(names) >= 10 {
			break
		}
	}
	return names
}

// isEndOfSentence checks if the word at index is at the end of a sentence
func isEndOfSentence(words []string, idx int) bool {
	if idx < 0 || idx >= len(words) {
		return false
	}
	word := words[idx]
	// Check if word ends with sentence-ending punctuation
	lastChar := []rune(word)[len([]rune(word))-1]
	return lastChar == '.' || lastChar == '!' || lastChar == '?'
}

// renderSpecialNames formats names with 🏷 prefix
func renderSpecialNames(names []string) string {
	if len(names) == 0 {
		return "Nessun nome speciale rilevato."
	}
	var b strings.Builder
	for _, n := range names {
		b.WriteString("   🏷 ")
		b.WriteString(n)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
