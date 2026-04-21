package scriptdocs

import (
	"sort"
	"strings"
	"unicode"

	"velox/go-master/internal/translation"
)

// extractClipKeywords extracts meaningful keywords from phrases, names, and words
// for dynamic clip searching. Focuses on visual concepts: events, actions, objects.
func (s *ScriptDocService) extractClipKeywords(frasi []string, nomi []string, parole []string) []string {
	seen := make(map[string]bool)
	var keywords []string
	translator := translation.NewClipSearchTranslator()
	conceptLookup := buildConceptKeywordLookup()
	banned := make(map[string]bool)
	for _, n := range nomi {
		nk := normalizeKeyword(n)
		if len(nk) >= 3 {
			banned[nk] = true
		}
	}
	for _, b := range []string{"gervonta", "davis", "sylvester", "baltimore", "greenmount"} {
		banned[b] = true
	}

	addKeyword := func(raw string) {
		kw := normalizeKeyword(raw)
		if len(kw) < 3 || seen[kw] || banned[kw] {
			return
		}
		translated := translator.TranslateKeywords([]string{kw})
		if len(translated) > 0 {
			kw = normalizeKeyword(translated[0])
		}
		if len(kw) < 3 || seen[kw] || banned[kw] {
			return
		}
		seen[kw] = true
		keywords = append(keywords, kw)
	}

	// 1) Concept terms first: stable and visual.
	contextLower := strings.ToLower(strings.Join(frasi, " "))
	type conceptScore struct {
		term  string
		score int
	}
	var conceptScores []conceptScore
	for _, cm := range conceptMap {
		score, _ := scoreConceptForPhrase(contextLower, cm)
		if score > 0 {
			conceptScores = append(conceptScores, conceptScore{term: cm.Term, score: score})
		}
	}
	sort.SliceStable(conceptScores, func(i, j int) bool {
		if conceptScores[i].score != conceptScores[j].score {
			return conceptScores[i].score > conceptScores[j].score
		}
		return len(conceptScores[i].term) > len(conceptScores[j].term)
	})
	for i, cs := range conceptScores {
		if i >= 3 {
			break
		}
		addKeyword(cs.term)
	}

	// 1b) Generic boxing-friendly boosters when combat context is detected.
	if looksLikeCombatContext(contextLower) {
		for _, kw := range []string{"boxing", "knockout", "training", "gym", "fight"} {
			addKeyword(kw)
			if len(keywords) >= 5 {
				break
			}
		}
	}

	// 2) Rank normalized tokens by frequency + concept affinity.
	type tokenScore struct {
		token string
		score int
	}
	counts := make(map[string]int)
	addTokens := func(text string) {
		for _, tok := range tokenizeWords(text) {
			if len(tok) >= 3 {
				counts[tok]++
			}
		}
	}
	for _, f := range frasi {
		addTokens(f)
	}
	// NOTE: do not inject proper nouns/person names into clip keywords.
	for _, p := range parole {
		addTokens(p)
	}

	var scored []tokenScore
	for tok, freq := range counts {
		score := freq * 3
		if conceptLookup[tok] {
			score += 8
		}
		if len(tok) >= 6 {
			score += 2
		}
		scored = append(scored, tokenScore{token: tok, score: score})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return len(scored[i].token) > len(scored[j].token)
	})

	for _, ts := range scored {
		if banned[ts.token] {
			continue
		}
		addKeyword(ts.token)
		if len(keywords) >= 5 {
			break
		}
	}
	return keywords
}

func looksLikeCombatContext(lowerText string) bool {
	hits := 0
	for _, tok := range []string{
		// EN
		"box", "boxing", "boxer", "fight", "knockout", "ring", "champion", "punch", "training", "gym", "match",
		// IT
		"boxe", "pugilato", "pugile", "combatt", "campion", "allenament", "incontro", "vittoria", "guant", "ko",
	} {
		if strings.Contains(lowerText, tok) {
			hits++
		}
	}
	return hits >= 2
}

func buildConceptKeywordLookup() map[string]bool {
	out := make(map[string]bool)
	for _, cm := range conceptMap {
		out[normalizeKeyword(cm.Term)] = true
		for _, kw := range cm.Keywords {
			n := normalizeKeyword(kw)
			if len(n) >= 3 {
				out[n] = true
			}
		}
	}
	return out
}

func tokenizeWords(text string) []string {
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		n := normalizeKeyword(p)
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}
