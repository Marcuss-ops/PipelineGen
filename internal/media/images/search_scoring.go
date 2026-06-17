package images

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

func normalizeLookupTerm(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("'", "'", "'", "'", "´", "'", "`", "'", "´", "'").Replace(value)
	value = strings.ToLower(value)
	value = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

func looksLikeProperName(query string) bool {
	query = strings.TrimSpace(strings.NewReplacer("'", "'", "'", "'").Replace(query))
	if query == "" {
		return false
	}

	parts := strings.Fields(query)
	if len(parts) == 0 || len(parts) > 5 {
		return false
	}

	capitalized := 0
	for _, part := range parts {
		part = strings.Trim(part, `"'.,;:!?()[]{}<>`)
		if part == "" {
			continue
		}
		r, _ := utf8.DecodeRuneInString(part)
		if unicode.IsUpper(r) {
			capitalized++
		}
	}

	if len(parts) == 1 {
		return capitalized == 1 && len(parts[0]) >= 4
	}

	return capitalized >= 1 || strings.ContainsAny(query, "''")
}

func selectBestWikidataHit(query string, hits []struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}) (string, string, string) {
	bestScore := 0
	bestLabel := ""
	bestID := ""
	bestDescription := ""
	for _, hit := range hits {
		score := scoreWikiCandidate(query, hit.Label)
		if score > bestScore {
			bestScore = score
			bestLabel = hit.Label
			bestID = hit.ID
			bestDescription = hit.Description
		}
	}
	if bestScore < minWikiScore(query) {
		return "", "", ""
	}
	return bestLabel, bestID, bestDescription
}

func selectBestWikiTitle(query string, hits []struct {
	Title string `json:"title"`
}) string {
	bestScore := 0
	bestTitle := ""
	for _, hit := range hits {
		score := scoreWikiCandidate(query, hit.Title)
		if score > bestScore {
			bestScore = score
			bestTitle = hit.Title
		}
	}
	if bestScore < minWikiScore(query) {
		return ""
	}
	return bestTitle
}

func minWikiScore(query string) int {
	if looksLikeProperName(query) {
		return 80
	}
	return 50
}

func scoreWikiCandidate(query, candidate string) int {
	qTokens := meaningfulLookupTokens(query)
	cTokens := meaningfulLookupTokens(candidate)
	if len(qTokens) == 0 || len(cTokens) == 0 {
		return 0
	}

	qNorm := strings.Join(qTokens, " ")
	cNorm := strings.Join(cTokens, " ")
	if qNorm == cNorm {
		return 100
	}

	if strings.HasPrefix(cNorm, qNorm) || strings.HasPrefix(qNorm, cNorm) {
		return 95
	}

	cTokenSet := make(map[string]struct{}, len(cTokens))
	for _, token := range cTokens {
		cTokenSet[token] = struct{}{}
	}

	matched := 0
	for _, token := range qTokens {
		if _, ok := cTokenSet[token]; ok {
			matched++
		}
	}

	if matched == 0 {
		return 0
	}

	score := matched * 20
	if len(qTokens) == 1 {
		if matched == 1 {
			score += 25
		}
		return score
	}

	if matched == len(qTokens) {
		score += 40
	}
	if qTokens[0] == cTokens[0] {
		score += 10
	}
	return score
}

func meaningfulLookupTokens(value string) []string {
	value = normalizeLookupTerm(value)
	if value == "" {
		return nil
	}

	stopwords := map[string]struct{}{
		"d": {}, "de": {}, "di": {}, "da": {}, "del": {}, "della": {}, "dello": {}, "degli": {}, "delle": {},
		"of": {}, "the": {}, "and": {}, "la": {}, "le": {}, "el": {}, "los": {}, "las": {}, "von": {}, "van": {},
	}

	parts := strings.Fields(value)
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 2 {
			continue
		}
		if _, ok := stopwords[part]; ok {
			continue
		}
		tokens = append(tokens, part)
	}
	return tokens
}
