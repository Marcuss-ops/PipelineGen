package association

import (
	"strings"

	"velox/go-master/internal/pkg/textutil"
)

// collectTerms estrae i termini di ricerca dalla richiesta.
func collectTerms(req CandidatesRequest) []string {
	terms := make([]string, 0)
	seen := make(map[string]struct{})
	add := func(text string) {
		for _, term := range textutil.Tokenize(text) {
			term = strings.TrimSpace(term)
			if term == "" || len(term) < 3 || textutil.IsStopWord(term) {
				continue
			}
			key := strings.ToLower(term)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			terms = append(terms, term)
		}
	}

	add(req.Topic)
	add(req.Subject)
	// add(req.Narrative) -- Removed: too broad for direct asset matching
	add(strings.Join(req.Keywords, " "))
	add(strings.Join(req.Entities, " "))

	return terms
}

// normalizeKey normalizza una chiave per il matching (lowercase, trim, rimpiazzo separatori).
func normalizeKey(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "_", " ")
	text = strings.ReplaceAll(text, "-", " ")
	text = strings.Join(strings.Fields(text), " ")
	return text
}
