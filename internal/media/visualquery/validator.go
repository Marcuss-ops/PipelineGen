package visualquery

import (
	"strings"
)

// isValidVisualQuery validates that a query meets the requirements
func isValidVisualQuery(query string) bool {
	if query == "" {
		return false
	}

	// Check for punctuation (except spaces and hyphens)
	for _, r := range query {
		if !isAllowedChar(r) {
			return false
		}
	}

	words := strings.Fields(query)

	// Check word count
	if len(words) < MinQueryWords || len(words) > MaxQueryWords {
		return false
	}

	// Check for banned words
	bannedWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "was": true,
		"were": true, "are": true, "been": true, "have": true, "has": true,
		"had": true, "but": true, "and": true, "or": true, "then": true,
		"they": true, "their": true, "further": true, "each": true,
		"continues": true, "beginning": true, "comprehend": true,
	}

	for _, w := range words {
		if bannedWords[strings.ToLower(w)] {
			return false
		}
	}

	return true
}

// isAllowedChar checks if a character is allowed in a query
func isAllowedChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == ' ' || r == '-'
}
