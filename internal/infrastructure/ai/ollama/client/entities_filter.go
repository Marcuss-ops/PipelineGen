package client

import (
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/linguistics"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func sanitizeEntityExtractionResult(segment string, result *asset.EntityExtractionResult, limit int) *asset.EntityExtractionResult {
	if result == nil {
		return nil
	}

	// Load the fallback lexicon profile once per sanitization pass so
	// every sub-filter uses the same centralized stopwords, function
	// words and verb suffixes instead of hardcoded maps.
	profile := linguistics.DefaultLexicon().Resolve("fallback")

	result.FrasiImportanti = filterExactPhrases(segment, result.FrasiImportanti, profile)
	result.NomiSpeciali = filterExactNames(segment, result.NomiSpeciali, profile)
	result.ParoleImportanti = filterExactWords(segment, result.ParoleImportanti, profile)
	result.NomiSpeciali = filterProperNouns(segment, result.NomiSpeciali, profile)
	result.ArtlistPhrases = filterArtlistKeywords(segment, result.ArtlistPhrases, profile)
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

func filterExactPhrases(segment string, items []string, profile *linguistics.LexiconProfile) []string {
	return filterExactStrings(segment, items, false, profile)
}

func filterExactNames(segment string, items []string, profile *linguistics.LexiconProfile) []string {
	return filterExactStrings(segment, items, true, profile)
}

func filterProperNouns(segment string, items []string, profile *linguistics.LexiconProfile) []string {
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
			if isSentenceStartCapitalizedOnly(words[0], segLower, profile) {
				continue
			}
		}
		if len(words) > 4 {
			continue
		}
		functionCount := 0
		for _, w := range words {
			if _, ok := profile.StopWords[strings.ToLower(w)]; ok {
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
				if isGenericImportantWord(strings.ToLower(base)) || isSentenceStartCapitalizedOnly(strings.ToLower(base), segLower, profile) {
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

func isSentenceStartCapitalizedOnly(word string, segLower string, profile *linguistics.LexiconProfile) bool {
	lower := strings.ToLower(word)
	if _, ok := profile.StopWords[lower]; !ok {
		return false
	}
	capCount := strings.Count(segLower, lower)
	return capCount >= 1
}

func filterExactWords(segment string, items []string, profile *linguistics.LexiconProfile) []string {
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
		if isNoisyExtractionCandidate(item) {
			continue
		}
		lower := strings.ToLower(item)
		if _, ok := profile.StopWords[lower]; ok {
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

func filterArtlistKeywords(segment string, items []string, profile *linguistics.LexiconProfile) []string {
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
			if _, ok := profile.StopWords[strings.ToLower(w)]; !ok && !isGenericImportantWord(strings.ToLower(w)) {
				allStop = false
				break
			}
		}
		if allStop {
			continue
		}

		firstWord := strings.ToLower(words[0])
		if isFunctionWord(firstWord, profile) {
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

		if looksLikeVerbBigram(words, profile) {
			continue
		}

		if hasMixedNumberAlpha(words) {
			continue
		}

		candidates = append(candidates, item)
	}
	if len(candidates) > 1 {
		candidates = removeSlidingWindowChains(candidates, profile)
		candidates = uniqueLocalStrings(candidates)
		if len(candidates) > 5 {
			candidates = candidates[:5]
		}
	}
	return candidates
}

func isFunctionWord(word string, profile *linguistics.LexiconProfile) bool {
	if word == "" {
		return false
	}
	_, ok := profile.FunctionWords[strings.ToLower(word)]
	return ok
}

func looksLikeVerbBigram(words []string, profile *linguistics.LexiconProfile) bool {
	if len(words) < 2 {
		return false
	}
	suffixes := profile.VerbSuffixes
	verbCount := 0
	for _, w := range words {
		lower := strings.ToLower(w)
		for _, suffix := range suffixes {
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

func removeSlidingWindowChains(items []string, profile *linguistics.LexiconProfile) []string {
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
		if looksLikeTextBigram(wordsA, profile) && looksLikeTextBigram(wordsB, profile) {
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

func looksLikeTextBigram(words []string, profile *linguistics.LexiconProfile) bool {
	if len(words) != 2 {
		return false
	}
	if isFunctionWord(words[0], profile) || isFunctionWord(words[1], profile) {
		return true
	}
	suffixes := profile.VerbSuffixes
	verbCount := 0
	for _, w := range words {
		lower := strings.ToLower(w)
		for _, suffix := range suffixes {
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

func filterExactStrings(segment string, items []string, names bool, profile *linguistics.LexiconProfile) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || isNoisyExtractionCandidate(item) {
			continue
		}
		if names && len(strings.Fields(item)) == 1 && (isGenericImportantWord(strings.ToLower(item)) || isStopWord(strings.ToLower(item), profile)) {
			continue
		}
		if textutil.ContainsCI(segment, item) {
			out = append(out, item)
		}
	}
	return uniqueLocalStrings(out)
}

func isStopWord(word string, profile *linguistics.LexiconProfile) bool {
	if word == "" {
		return false
	}
	_, ok := profile.StopWords[word]
	return ok
}
