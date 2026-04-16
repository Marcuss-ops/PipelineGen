package scriptdocs

import (
	"strings"
	"unicode"
)

// extractClipKeywords extracts meaningful keywords from phrases, names, and words
// for dynamic clip searching. Focuses on visual concepts: events, actions, objects.
func (s *ScriptDocService) extractClipKeywords(frasi []string, nomi []string, parole []string) []string {
	seen := make(map[string]bool)
	var keywords []string

	// Skip words that are too generic for clip searching
	skipWords := map[string]bool{
		"the": true, "and": true, "but": true, "his": true, "her": true,
		"with": true, "from": true, "that": true, "this": true, "was": true,
		"has": true, "have": true, "had": true, "were": true, "been": true,
		"also": true, "when": true, "than": true, "then": true, "after": true,
		"before": true, "during": true, "while": true, "where": true, "who": true,
		"which": true, "what": true, "how": true, "why": true, "very": true,
		"more": true, "most": true, "some": true, "any": true, "all": true,
		"each": true, "every": true, "both": true, "few": true, "many": true,
		"much": true, "other": true, "another": true, "such": true, "only": true,
	}

	// Skip short or generic proper nouns
	skipNames := map[string]bool{
		"he": true, "she": true, "it": true, "january": true, "february": true,
		"march": true, "april": true, "may": true, "june": true, "july": true,
		"august": true, "september": true, "october": true, "november": true,
		"december": true, "monday": true, "tuesday": true, "wednesday": true,
		"thursday": true, "friday": true, "saturday": true, "sunday": true,
	}

	// 1. Extract multi-word proper nouns first (highest priority for clips)
	for _, name := range nomi {
		lower := strings.ToLower(name)
		if skipNames[lower] || len(name) < 4 {
			continue
		}
		if !seen[lower] {
			seen[lower] = true
			keywords = append(keywords, name)
		}
	}

	// 2. Extract visual keywords from phrases (look for action/event words)
	for _, frase := range frasi {
		words := strings.Fields(frase)
		for _, word := range words {
			clean := strings.TrimFunc(word, func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r)
			})
			lower := strings.ToLower(clean)
			if len(clean) < 4 || skipWords[lower] {
				continue
			}
			// Prioritize nouns and action words (simplified heuristic)
			if unicode.IsUpper(rune(clean[0])) || strings.HasSuffix(lower, "ing") ||
				strings.HasSuffix(lower, "tion") || strings.HasSuffix(lower, "ment") {
				if !seen[lower] {
					seen[lower] = true
					keywords = append(keywords, clean)
				}
			}
		}
	}

	// 3. Add important single words as backup
	for _, parola := range parole {
		lower := strings.ToLower(parola)
		if skipWords[lower] || len(parola) < 5 {
			continue
		}
		if !seen[lower] {
			seen[lower] = true
			keywords = append(keywords, parola)
		}
	}

	// Limit to max 5 keywords for efficiency
	if len(keywords) > 5 {
		keywords = keywords[:5]
	}

	return keywords
}
