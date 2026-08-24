package client

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func fallbackEntityExtractionResult(segment string, segmentIndex, entityCount int, language string) *asset.EntityExtractionResult {
	segment = strings.TrimSpace(segment)
	if entityCount <= 0 {
		entityCount = 2
	}
	result := &asset.EntityExtractionResult{
		SegmentIndex:     segmentIndex,
		FrasiImportanti:  []string{},
		EntitaSenzaTesto: make(map[string]string),
		NomiSpeciali:     []string{},
		ParoleImportanti: []string{},
		ArtlistPhrases:   []string{},
		Source:           "heuristic_fallback",
	}

	sentences := splitSentences(segment)
	for i, s := range sentences {
		if i >= 5 {
			break
		}
		s = strings.TrimSpace(s)
		if len(s) > 20 && len(s) < 300 {
			result.FrasiImportanti = append(result.FrasiImportanti, s)
		}
	}

	result.ParoleImportanti = fallbackImportantWords(segment, entityCount, language)
	result.NomiSpeciali = fallbackSpecialNames(segment, entityCount, language)
	result.ArtlistPhrases = fallbackArtlistPhrases(segment, entityCount)
	return result
}

// phraseWordRE matches a word while preserving its original casing, so proper
// nouns stay detectable as salient anchors for the important-phrase heuristic.
var phraseWordRE = regexp.MustCompile(`[\p{L}\p{M}]+(?:['’][\p{L}\p{M}]+)?`)

const (
	// minImportantPhraseWords / maxImportantPhraseWords bound the salient
	// important phrases the fallback emits. A phrase outside this window is
	// either a single word (a keyword, not a phrase) or a whole sentence /
	// paragraph, which must never be surfaced as an important phrase.
	minImportantPhraseWords = 2
	maxImportantPhraseWords = 4
)

// fallbackImportantPhrases extracts short, salient, verbatim phrases (2–4
// words) from the segment instead of whole sentences or paragraphs. It anchors
// on proper nouns and content words and processes comma-separated clauses
// independently, so unrelated fragments are never glued together.
func fallbackImportantPhrases(segment string, limit int, profile *linguistics.LexiconProfile) []string {
	if limit <= 0 {
		limit = 5
	}
	if profile == nil {
		profile = linguistics.DefaultLexicon().Resolve("fallback")
	}
	var out []string
	seen := make(map[string]struct{})
	for _, sentence := range splitSentences(segment) {
		for _, phrase := range salientPhrases(sentence, profile) {
			key := strings.ToLower(phrase)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, phrase)
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}

// salientPhrases extracts salient phrases from each comma-separated clause of
// one sentence. Clauses are processed independently so a comma never joins two
// unrelated fragments (e.g. "speculation Cody Rhodes").
func salientPhrases(sentence string, profile *linguistics.LexiconProfile) []string {
	var out []string
	for _, clause := range splitClauses(sentence) {
		out = append(out, salientClausePhrases(phraseWordRE.FindAllString(clause, -1), profile)...)
	}
	return out
}

func splitClauses(sentence string) []string {
	return strings.FieldsFunc(sentence, func(r rune) bool {
		switch r {
		case ',', ';', ':', '—', '–':
			return true
		}
		return false
	})
}

type phraseCandidate struct {
	text  string
	score int
	start int
	end   int
}

// salientClausePhrases returns non-overlapping, salient 2–4 word windows of a
// clause. A window is salient when it neither starts nor ends with a stop/function
// word and contains at least one content word; proper nouns (title-cased words
// that are not clause-initial) are the strongest anchors.
func salientClausePhrases(words []string, profile *linguistics.LexiconProfile) []string {
	if len(words) < minImportantPhraseWords {
		return nil
	}
	stop := func(w string) bool {
		l := strings.ToLower(w)
		_, isStop := profile.StopWords[l]
		_, isFunc := profile.FunctionWords[l]
		return isStop || isFunc
	}
	content := func(w string) bool {
		return !stop(w) && len([]rune(w)) >= 3
	}
	proper := func(i int) bool {
		if i == 0 {
			return false // clause-initial capitalization is not evidence of a name
		}
		r := []rune(words[i])
		return len(r) > 0 && unicode.IsUpper(r[0]) && !stop(words[i])
	}

	var candidates []phraseCandidate
	for i := 0; i < len(words); i++ {
		for n := minImportantPhraseWords; n <= maxImportantPhraseWords && i+n <= len(words); n++ {
			window := words[i : i+n]
			if stop(window[0]) || stop(window[n-1]) {
				continue
			}
			contentCount, properCount := 0, 0
			for j := 0; j < n; j++ {
				if content(window[j]) {
					contentCount++
				}
				if proper(i + j) {
					properCount++
				}
			}
			if contentCount == 0 {
				continue
			}
			candidates = append(candidates, phraseCandidate{
				text:  strings.Join(window, " "),
				score: properCount*5 + contentCount*2 + n,
				start: i,
				end:   i + n - 1,
			})
		}
	}
	sort.SliceStable(candidates, func(a, b int) bool {
		if candidates[a].score != candidates[b].score {
			return candidates[a].score > candidates[b].score
		}
		if candidates[a].start != candidates[b].start {
			return candidates[a].start < candidates[b].start
		}
		return candidates[a].end < candidates[b].end
	})

	var out []string
	seen := map[string]struct{}{}
	occupied := map[int]bool{}
	for _, c := range candidates {
		key := strings.ToLower(c.text)
		if _, dup := seen[key]; dup {
			continue
		}
		overlap := false
		for p := c.start; p <= c.end; p++ {
			if occupied[p] {
				overlap = true
				break
			}
		}
		if overlap {
			continue
		}
		seen[key] = struct{}{}
		for p := c.start; p <= c.end; p++ {
			occupied[p] = true
		}
		out = append(out, c.text)
	}
	return out
}

// titleCaseWordRE matches a capitalized word whose second letter is lowercase
// (Unicode-aware). It runs against the case-preserved segment because a proper
// noun is only recoverable from capitalization: textutil.Tokenize lowercases
// its input, which previously made a case-sensitive capitalized-word pattern
// here unreachable and left this heuristic permanently empty.
var titleCaseWordRE = regexp.MustCompile(`\b[\p{Lu}][\p{Ll}][\p{L}'’-]*\b`)

func fallbackSpecialNames(segment string, limit int, language string) []string {
	if limit <= 0 {
		limit = 5
	}
	seen := make(map[string]struct{})
	var out []string
	for _, match := range titleCaseWordRE.FindAllString(segment, -1) {
		lower := strings.ToLower(match)
		if len([]rune(match)) < 3 {
			continue
		}
		if linguistics.IsStopWordForLanguage(lower, language) || linguistics.IsStopWord(lower) {
			continue
		}
		// A word that also occurs lowercase elsewhere is a common noun
		// capitalized only by sentence position, not a proper noun.
		if wordOccursLowercase(segment, lower) {
			continue
		}
		// A word that only ever starts a sentence is a common noun too.
		if !occursMidSentence(segment, match) {
			continue
		}
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		out = append(out, match)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// wordOccursLowercase reports whether lower appears as a standalone lowercase
// word in the case-preserved text. A proper noun stays capitalized everywhere;
// a common noun that also appears lowercase is not a name.
func wordOccursLowercase(segment, lower string) bool {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(lower) + `\b`)
	return re.MatchString(segment)
}

// occursMidSentence reports whether word appears at least once in a position
// that is not the start of a sentence.
func occursMidSentence(segment, word string) bool {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`)
	for _, span := range re.FindAllStringIndex(segment, -1) {
		if !isSentenceStart(segment, span[0]) {
			return true
		}
	}
	return false
}

// isSentenceStart reports whether byteIndex sits at the start of a sentence
// (start of text, or immediately after a sentence terminator, ignoring
// intervening whitespace).
func isSentenceStart(segment string, byteIndex int) bool {
	runes := []rune(segment[:byteIndex])
	i := len(runes) - 1
	for i >= 0 && (runes[i] == ' ' || runes[i] == '\t' || runes[i] == '\n' || runes[i] == '\r') {
		i--
	}
	if i < 0 {
		return true
	}
	switch runes[i] {
	case '.', '!', '?', '\n':
		return true
	}
	return false
}

func fallbackArtlistPhrases(segment string, limit int) []string {
	if limit <= 0 {
		limit = 5
	}
	if limit > 5 {
		limit = 5
	}
	profile := linguistics.DefaultLexicon().Resolve("fallback")
	words := textutil.Tokenize(segment)
	if len(words) < 4 {
		return nil
	}
	var out []string
	seen := make(map[string]struct{})
	// Skip first 2 words to avoid sentence-start function words
	startIdx := 0
	for i := 0; i < len(words) && i < 3; i++ {
		if !linguistics.IsStopWord(strings.ToLower(words[i])) {
			startIdx = i
			break
		}
	}
	for i := startIdx; i < len(words)-1 && len(out) < limit; i++ {
		w1 := strings.ToLower(words[i])
		w2 := strings.ToLower(words[i+1])

		// Skip if either word is a stop word
		if linguistics.IsStopWord(w1) || linguistics.IsStopWord(w2) {
			continue
		}
		// Skip if either word is a function word (article, preposition, etc.)
		if isFunctionWord(w1, profile) || isFunctionWord(w2, profile) {
			continue
		}
		// Skip if either word has apostrophe (contraction fragments)
		if strings.Contains(words[i], "'") || strings.Contains(words[i], "’") || strings.Contains(words[i+1], "'") || strings.Contains(words[i+1], "’") {
			continue
		}
		// Skip single-letter words
		if len(words[i]) <= 1 || len(words[i+1]) <= 1 {
			continue
		}

		phrase := w1 + " " + w2
		if _, ok := seen[phrase]; ok {
			continue
		}
		seen[phrase] = struct{}{}
		out = append(out, words[i]+" "+words[i+1])
	}
	return out
}

func extractSignificantWords(text string, limit int, language string) []string {
	words := textutil.Tokenize(text)
	seen := make(map[string]struct{})
	var out []string
	for _, w := range words {
		wLower := strings.ToLower(w)
		if len(w) < 3 || linguistics.IsStopWord(wLower) || linguistics.IsStopWordForLanguage(wLower, language) {
			continue
		}
		if _, ok := seen[wLower]; ok {
			continue
		}
		seen[wLower] = struct{}{}
		out = append(out, w)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func fallbackImportantWords(segment string, limit int, language string) []string {
	if limit <= 0 {
		limit = 5
	}
	return extractSignificantWords(segment, limit, language)
}
