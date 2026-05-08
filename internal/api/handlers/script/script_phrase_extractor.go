package script

import "strings"

// extractImportantPhrases splits narrative into sentences, returns up to 10 unique phrases
func extractImportantPhrases(narrative string) []string {
	sentences := strings.FieldsFunc(narrative, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	})
	var phrases []string
	seen := make(map[string]struct{})
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		s = strings.TrimRight(s, ".!?")
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		phrases = append(phrases, s)
		if len(phrases) >= 10 {
			break
		}
	}
	return phrases
}

// renderImportantPhrases formats phrases with ✨ prefix
func renderImportantPhrases(phrases []string) string {
	if len(phrases) == 0 {
		return "Nessuna frase importante rilevata."
	}
	var b strings.Builder
	for _, p := range phrases {
		b.WriteString("   ✨ \"")
		b.WriteString(p)
		b.WriteString("\"\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
