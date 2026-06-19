package client

import (
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/core"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func sanitizeEntityExtractionResult(segment string, result *core.EntityExtractionResult, limit int) *core.EntityExtractionResult {
	if result == nil {
		return nil
	}

	result.FrasiImportanti = filterExactPhrases(segment, result.FrasiImportanti)
	result.NomiSpeciali = filterExactNames(segment, result.NomiSpeciali)
	result.ParoleImportanti = filterExactWords(segment, result.ParoleImportanti)
	result.NomiSpeciali = filterProperNouns(segment, result.NomiSpeciali)
	result.ArtlistPhrases = filterArtlistKeywords(segment, result.ArtlistPhrases)
	result.EntitaSenzaTesto = filterExactEntityMap(segment, result.EntitaSenzaTesto)

	if len(result.FrasiImportanti) == 0 {
		result.FrasiImportanti = fallbackImportantPhrases(segment, limit)
	}
	if len(result.NomiSpeciali) == 0 {
		result.NomiSpeciali = fallbackSpecialNames(segment, limit)
	}
	if len(result.ParoleImportanti) == 0 {
		result.ParoleImportanti = fallbackImportantWords(segment, limit)
	}
	if len(result.ArtlistPhrases) == 0 {
		result.ArtlistPhrases = fallbackArtlistPhrases(segment, limit)
	}
	if len(result.EntitaSenzaTesto) == 0 && len(result.NomiSpeciali) > 0 {
		result.EntitaSenzaTesto = make(map[string]string, len(result.NomiSpeciali))
		for _, name := range result.NomiSpeciali {
			result.EntitaSenzaTesto[name] = ""
		}
	}
	return result
}

func filterExactPhrases(segment string, items []string) []string {
	return filterExactStrings(segment, items, false)
}

func filterExactNames(segment string, items []string) []string {
	return filterExactStrings(segment, items, true)
}

func filterProperNouns(segment string, items []string) []string {
	if len(items) == 0 {
		return nil
	}
	segLower := strings.ToLower(segment)
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		words := strings.Fields(item)
		if len(words) == 1 {
			if isSentenceStartCapitalizedOnly(words[0], segLower) {
				continue
			}
		}
		if len(words) > 4 {
			continue
		}
		functionCount := 0
		for _, w := range words {
			if textutil.IsStopWord(strings.ToLower(w)) {
				functionCount++
			}
		}
		if len(words) > 0 && functionCount*2 > len(words) {
			continue
		}
		if strings.Contains(item, ",") && len(words) > 3 {
			continue
		}
		for _, w := range words {
			if strings.HasSuffix(w, "'s") || strings.HasSuffix(w, "'") {
				base := strings.TrimSuffix(strings.TrimSuffix(w, "'s"), "'")
				if isGenericImportantWord(strings.ToLower(base)) || isSentenceStartCapitalizedOnly(strings.ToLower(base), segLower) {
					goto skipItem
				}
			}
		}
		out = append(out, item)
		continue
	skipItem:
	}
	return uniqueLocalStrings(out)
}

var sentenceStartStopWords = map[string]struct{}{
	"the": {}, "this": {}, "that": {}, "these": {}, "those": {},
	"when": {}, "where": {}, "why": {}, "how": {}, "what": {}, "which": {}, "who": {},
	"yet": {}, "still": {}, "then": {}, "now": {}, "here": {}, "there": {},
	"subsequent": {}, "subsequently": {}, "previous": {}, "previously": {},
	"following": {}, "next": {}, "before": {}, "after": {}, "during": {},
	"mount": {}, "mountains": {}, "river": {}, "ocean": {}, "sea": {},
	"august": {}, "september": {}, "october": {}, "november": {}, "december": {},
	"january": {}, "february": {}, "march": {}, "april": {}, "may": {}, "june": {}, "july": {},
	"monday": {}, "tuesday": {}, "wednesday": {}, "thursday": {}, "friday": {}, "saturday": {}, "sunday": {},
	"north": {}, "south": {}, "east": {}, "west": {},
	"first": {}, "second": {}, "third": {}, "last": {},
	"one": {}, "two": {}, "three": {}, "four": {}, "five": {},
	"many": {}, "most": {}, "some": {}, "all": {}, "each": {}, "every": {},
	"such": {}, "other": {}, "another": {},
	"behind": {}, "beneath": {}, "beyond": {}, "above": {}, "below": {}, "within": {}, "outside": {},
	"despite": {}, "although": {}, "because": {}, "since": {}, "unless": {}, "while": {},
	"through": {}, "across": {}, "around": {}, "along": {}, "between": {},
	"again": {}, "also": {}, "perhaps": {}, "possibly": {}, "indeed": {},
	"though": {}, "however": {}, "moreover": {}, "furthermore": {}, "nevertheless": {},
	"meanwhile": {}, "otherwise": {}, "instead": {}, "thus": {}, "therefore": {},
	"hence": {}, "consequently": {}, "accordingly": {}, "similarly": {},
	"likewise": {}, "notably": {}, "importantly": {}, "surprisingly": {},
	"finally": {}, "ultimately": {}, "eventually": {}, "gradually": {},
	"essentially": {}, "basically": {}, "primarily": {}, "mainly": {},
	"particularly": {}, "especially": {}, "specifically": {}, "generally": {},
	"typically": {}, "usually": {}, "normally": {}, "commonly": {},
	"widely": {}, "deeply": {}, "greatly": {}, "strongly": {}, "highly": {},
	"western": {}, "eastern": {}, "northern": {}, "southern": {},
	"ancient": {}, "modern": {}, "new": {}, "old": {}, "young": {},
	"great": {}, "small": {}, "large": {}, "long": {}, "short": {},
	"law": {}, "laws": {}, "legal": {}, "religion": {}, "religions": {},
	"culture": {}, "cultures": {}, "society": {}, "societies": {},
	"empire": {}, "kingdom": {}, "republic": {}, "civilization": {},
	"people": {}, "nations": {}, "peoples": {}, "world": {}, "history": {},
}

func isSentenceStartCapitalizedOnly(word string, segLower string) bool {
	lower := strings.ToLower(word)
	_, isStop := sentenceStartStopWords[lower]
	if !isStop {
		return false
	}
	capCount := strings.Count(segLower, lower)
	return capCount >= 1
}

func filterExactWords(segment string, items []string) []string {
	if len(items) == 0 {
		return nil
	}
	segmentTokens := tokenSet(segment)
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if isNoisyExtractionCandidate(item) || textutil.IsStopWord(strings.ToLower(item)) {
			continue
		}
		tokens := textutil.Tokenize(item)
		if len(tokens) == 0 {
			continue
		}
		ok := true
		for _, tok := range tokens {
			if _, exists := segmentTokens[tok]; !exists {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, item)
		}
	}
	return uniqueLocalStrings(out)
}

func filterArtlistKeywords(segment string, items []string) []string {
	if len(items) == 0 {
		return nil
	}
	var candidates []string
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		words := strings.Fields(item)
		if len(words) < 2 || len(words) > 3 {
			continue
		}

		allStop := true
		for _, w := range words {
			if !textutil.IsStopWord(strings.ToLower(w)) && !isGenericImportantWord(strings.ToLower(w)) {
				allStop = false
				break
			}
		}
		if allStop {
			continue
		}

		firstWord := strings.ToLower(words[0])
		if isFunctionWord(firstWord) {
			continue
		}

		hasApostrophe := false
		for _, w := range words {
			if strings.Contains(w, "'") || strings.Contains(w, "\u2019") || strings.Contains(w, "`") {
				hasApostrophe = true
				break
			}
		}
		if hasApostrophe {
			continue
		}

		hasSingleLetter := false
		for _, w := range words {
			if len(w) == 1 && !unicode.IsDigit(rune(w[0])) {
				hasSingleLetter = true
				break
			}
		}
		if hasSingleLetter {
			continue
		}

		if looksLikeVerbBigram(words) {
			continue
		}

		if hasMixedNumberAlpha(words) {
			continue
		}

		candidates = append(candidates, item)
	}
	if len(candidates) > 1 {
		candidates = removeSlidingWindowChains(candidates)
		candidates = uniqueLocalStrings(candidates)
		if len(candidates) > 5 {
			candidates = candidates[:5]
		}
	}
	return candidates
}

// isFunctionWord returns true for Italian/English function words that should NOT
// start an artlist phrase (prepositions, articles, pronouns, possessive adjectives).
func isFunctionWord(word string) bool {
	functionWords := map[string]struct{}{
		"il": {}, "lo": {}, "la": {}, "i": {}, "gli": {}, "le": {},
		"un": {}, "uno": {}, "una": {},
		"del": {}, "dello": {}, "della": {}, "dei": {}, "degli": {}, "delle": {},
		"al": {}, "allo": {}, "alla": {}, "ai": {}, "agli": {}, "alle": {},
		"dal": {}, "dallo": {}, "dalla": {}, "dai": {}, "dagli": {}, "dalle": {},
		"nel": {}, "nello": {}, "nella": {}, "nei": {}, "negli": {}, "nelle": {},
		"sul": {}, "sullo": {}, "sulla": {}, "sui": {}, "sugli": {}, "sulle": {},
		"col": {}, "collo": {}, "colla": {}, "coi": {}, "cogli": {}, "colle": {},
		"pel": {}, "pello": {}, "pella": {}, "pei": {}, "pegli": {}, "pelle": {},
		"su": {}, "per": {}, "con": {}, "tra": {}, "fra": {}, "di": {}, "da": {}, "che": {}, "chi": {},
		"questo": {}, "questa": {}, "questi": {}, "queste": {}, "quest'": {},
		"quello": {}, "quella": {}, "quelli": {}, "quelle": {},
		"suo": {}, "sua": {}, "suoi": {}, "sue": {},
		"mio": {}, "mia": {}, "tuo": {}, "tua": {}, "nostro": {}, "nostra": {},
		"the": {}, "an": {}, "of": {}, "on": {}, "at": {}, "by": {}, "for": {}, "with": {}, "from": {}, "to": {}, "its": {}, "his": {}, "her": {}, "their": {},
	}
	_, ok := functionWords[word]
	return ok
}

// looksLikeVerbBigram checks if ALL words in the phrase look like verb forms
// (ending in common Italian verb suffixes). Verb-only bigrams from the text
// are not visual concepts and should be rejected.
func looksLikeVerbBigram(words []string) bool {
	if len(words) < 2 {
		return false
	}
	verbSuffixes := []string{
		"are", "ere", "ire", "ano", "ono", "ino",
		"ato", "uto", "ito", "ando", "endo",
		"ava", "eva", "iva", "avo", "evo", "ivo",
		"asse", "esse", "isse",
	}
	verbCount := 0
	for _, w := range words {
		lower := strings.ToLower(w)
		for _, suffix := range verbSuffixes {
			if strings.HasSuffix(lower, suffix) {
				verbCount++
				break
			}
		}
	}
	return verbCount == len(words)
}

// hasMixedNumberAlpha checks if the phrase mixes numbers and letters nonsensically
// (e.g. "79 d" from "79 d.C.", "1748 grazie" from "1748, grazie").
func hasMixedNumberAlpha(words []string) bool {
	hasNumber := false
	hasAlpha := false
	for _, w := range words {
		if isNumericWord(w) {
			hasNumber = true
		} else {
			hasAlpha = true
		}
	}
	return hasNumber && hasAlpha
}

// isNumericWord returns true if the word is purely numeric (digits, dots, dashes).
func isNumericWord(w string) bool {
	for _, r := range w {
		if !unicode.IsDigit(r) && r != '.' && r != '-' && r != '/' {
			return false
		}
	}
	return len(w) > 0
}

func removeSlidingWindowChains(items []string) []string {
	if len(items) <= 1 {
		return items
	}
	isChain := make([]bool, len(items))
	for i := 0; i < len(items)-1; i++ {
		if isChain[i] {
			continue
		}
		wordsA := strings.Fields(strings.ToLower(items[i]))
		if len(wordsA) == 0 {
			continue
		}
		lastWord := wordsA[len(wordsA)-1]
		for j := i + 1; j < len(items); j++ {
			wordsB := strings.Fields(strings.ToLower(items[j]))
			if len(wordsB) == 0 {
				continue
			}
			if wordsB[0] == lastWord {
				isChain[j] = true
				lastWord = wordsB[len(wordsB)-1]
			}
		}
	}

	// Detect non-overlapping sequential chains (e.g. "pompei prospero", "secoli diventando")
	// where consecutive items look like raw text bigrams but don't overlap.
	for i := 0; i < len(items)-1; i++ {
		if isChain[i] {
			continue
		}
		wordsA := strings.Fields(strings.ToLower(items[i]))
		wordsB := strings.Fields(strings.ToLower(items[i+1]))
		if len(wordsA) != 2 || len(wordsB) != 2 {
			continue
		}
		if looksLikeTextBigram(wordsA) && looksLikeTextBigram(wordsB) {
			isChain[i+1] = true
		}
	}

	out := make([]string, 0, len(items))
	for i, item := range items {
		if !isChain[i] {
			out = append(out, item)
		}
	}
	return out
}

// looksLikeTextBigram checks if a 2-word phrase looks like it was extracted
// directly as adjacent words from text rather than being a deliberate visual concept.
func looksLikeTextBigram(words []string) bool {
	if len(words) != 2 {
		return false
	}
	if isFunctionWord(words[0]) || isFunctionWord(words[1]) {
		return true
	}
	verbSuffixes := []string{
		"are", "ere", "ire", "ano", "ono", "ino",
		"ato", "uto", "ito", "ando", "endo",
		"ava", "eva", "iva", "avo", "evo", "ivo",
	}
	verbCount := 0
	for _, w := range words {
		lower := strings.ToLower(w)
		for _, suffix := range verbSuffixes {
			if strings.HasSuffix(lower, suffix) {
				verbCount++
				break
			}
		}
	}
	return verbCount >= 1
}

func filterExactEntityMap(segment string, items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for name, url := range items {
		name = strings.TrimSpace(name)
		if name == "" || isNoisyExtractionCandidate(name) {
			continue
		}
		if textutil.ContainsCI(segment, name) {
			out[name] = strings.TrimSpace(url)
		}
	}
	return out
}

func filterExactStrings(segment string, items []string, names bool) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || isNoisyExtractionCandidate(item) {
			continue
		}
		if names && len(strings.Fields(item)) == 1 && (isGenericImportantWord(strings.ToLower(item)) || textutil.IsStopWord(strings.ToLower(item))) {
			continue
		}
		if textutil.ContainsCI(segment, item) {
			out = append(out, item)
		}
	}
	return uniqueLocalStrings(out)
}
