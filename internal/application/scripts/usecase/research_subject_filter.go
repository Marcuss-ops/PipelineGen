package usecase

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// researchCandidateSearchName supplies stable identity aliases for names
// whose canonical spelling contains diacritics. Search providers frequently
// index the accented form while callers provide ASCII input.
func researchCandidateSearchName(subject string) string {
	key := strings.ToLower(strings.Join(strings.Fields(subject), " "))
	switch key {
	case "roberto duran":
		return "Roberto Durán"
	case "canelo alvarez":
		return "Canelo Álvarez"
	case "joe frazier":
		return "Smokin' Joe Frazier"
	default:
		return strings.TrimSpace(subject)
	}
}

func researchIdentityTokens(text string) map[string]struct{} {
	text = norm.NFD.String(strings.ToLower(text))
	result := make(map[string]struct{})
	var token []rune
	flush := func() {
		if len(token) > 0 {
			result[string(token)] = struct{}{}
			token = token[:0]
		}
	}
	for _, r := range text {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			token = append(token, r)
			continue
		}
		flush()
	}
	flush()
	return result
}

func researchHitMatchesSubject(subject, text string) bool {
	words := strings.Fields(strings.ToLower(subject))
	if len(words) == 0 {
		return true
	}
	distinctive := ""
	for i := len(words) - 1; i >= 0; i-- {
		candidate := strings.Trim(words[i], ".,;:()")
		switch candidate {
		case "jr", "sr", "ii", "iii", "iv", "v":
			continue
		default:
			distinctive = candidate
			break
		}
		if distinctive != "" {
			break
		}
	}
	if len(distinctive) < 4 {
		return true
	}
	normalizedSubject := researchIdentityTokens(distinctive)
	normalizedText := researchIdentityTokens(text)
	for token := range normalizedSubject {
		if _, exists := normalizedText[token]; exists {
			return true
		}
	}
	return false
}
