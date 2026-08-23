package usecase

import (
	"strings"
	"unicode"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"golang.org/x/text/unicode/norm"
)

// researchCandidateSearchName supplies stable identity aliases for names
// whose canonical spelling contains diacritics. Search providers frequently
// index the accented form while callers provide ASCII input.
//
// NOTE: this function is DEPRECATED for new code. Use
// SubjectIdentityResolver.Resolve() instead, which carries the full
// identity registry. Retained for backward compatibility with existing
// test callers.
func researchCandidateSearchName(subject string) string {
	resolver := NewSubjectIdentityResolver()
	identity := resolver.Resolve(subject)
	return identity.CanonicalName
}

// researchSubjectIdentity returns the canonical identity for a subject.
// This is the single entry point for identity resolution in the research
// pipeline — all callers should use this instead of hardcoded switches.
func researchSubjectIdentity(subject string) scriptpkg.SubjectIdentity {
	return NewSubjectIdentityResolver().Resolve(subject)
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

// researchHitMatchesSubject checks whether a search hit matches the
// given subject. It uses diacritic-normalized token overlap and respects
// the SubjectIdentity's excluded terms for disambiguation.
func researchHitMatchesSubject(subject, text string) bool {
	identity := researchSubjectIdentity(subject)
	return researchHitMatchesIdentity(identity, text)
}

// researchHitMatchesIdentity checks whether text matches the given
// SubjectIdentity. It uses the identity's excluded terms first (reject),
// then falls back to token-based matching on the canonical name.
func researchHitMatchesIdentity(identity scriptpkg.SubjectIdentity, text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, excl := range identity.ExcludedTerms {
		if strings.Contains(lower, strings.ToLower(excl)) {
			return false
		}
	}
	words := strings.Fields(strings.ToLower(identity.CanonicalName))
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
