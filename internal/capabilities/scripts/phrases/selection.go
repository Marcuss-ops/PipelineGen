// Package phrases owns the deterministic important-phrase / important-word
// selection used by every scene-text consumer (source enrichment, translated
// NLP): short, source-grounded editorial fragments ranked by configured
// lexicon strength, with no model, network or wall-clock input.
//
// It is a LEAF package: it depends only on the linguistics lexicon and takes a
// neutral text + blocked-rune-span input. Entity grounding stays with the
// caller that owns the entity model, so this package never imports the scripts
// capability root and the selection stays a pure function of its inputs.
//
// Extracted from internal/capabilities/scripts to keep that registered hotspot
// from growing: the phrase-ownership cutover added the selector there and
// pushed the package one file over its ratcheted baseline.
package phrases

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// importantPhraseWord matches source words while retaining their byte spans.
// Keeping the spans lets the selector return the exact original surface,
// including accents, apostrophes, and punctuation between words.
var importantPhraseWord = regexp.MustCompile(`[\p{L}\p{M}]+(?:['’][\p{L}\p{M}]+)?`)

type importantPhraseToken struct {
	text       string
	start, end int
	group      int
}

type importantPhraseCandidate struct {
	text          string
	start         int
	tokenStart    int
	tokenEnd      int
	score         int
	sentenceCount int
}

type importantPhraseSentence struct {
	byteStart  int
	byteEnd    int
	tokenStart int
	tokenEnd   int
}

// ImportantPhrases returns source-grounded phrase cards in editorial-strength
// order. Arbitrary fragments stay short, while complete sentences and adjacent
// sentence pairs may retain their full surface. It uses only configured
// stop/function words and visual verbs; it makes no model or network calls and
// never rewrites text.
//
// blockedSpans are the RUNE ranges the selection must not overlap — the caller
// passes the entity surfaces it already grounded so a phrase can never cover a
// name the entity overlays own.
func ImportantPhrases(text string, blockedSpans [][2]int, limit int, language string) []string {
	profile := importantPhraseLexicon(language)
	return Select(text, blockedSpans, limit, profile)
}

// ImportantPhrasesWithCorpus is ImportantPhrases with a document-wide
// term-frequency boost: corpus is every text of the SAME document (all scenes,
// one language). A candidate that recurs across the document outranks an
// otherwise-equal one that appears once. The selection stays deterministic and
// makes no model or network call.
func ImportantPhrasesWithCorpus(text string, blockedSpans [][2]int, limit int, language string, corpus []string) []string {
	profile := importantPhraseLexicon(language)
	return SelectWithCorpus(text, blockedSpans, limit, profile, corpus)
}

// Select is ImportantPhrases against an explicit lexicon profile. A nil profile
// falls back to the canonical phrase-extraction policy with no stop-word or
// visual-verb configuration.
func Select(text string, blockedSpans [][2]int, limit int, profile *linguistics.LexiconProfile) []string {
	return selectPhrases(text, blockedSpans, limit, profile, nil)
}

// SelectWithCorpus is Select with the document-wide term-frequency boost. A nil
// or empty corpus preserves Select exactly.
func SelectWithCorpus(text string, blockedSpans [][2]int, limit int, profile *linguistics.LexiconProfile, corpus []string) []string {
	return selectPhrases(text, blockedSpans, limit, profile, corpus)
}

func selectPhrases(text string, blockedSpans [][2]int, limit int, profile *linguistics.LexiconProfile, corpus []string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	policy := linguistics.DefaultPhraseExtractionPolicy()
	if profile != nil {
		policy = profile.PhrasePolicy
	}
	if policy.MinWords < 2 {
		policy.MinWords = 2
	}
	if policy.MaxWords < policy.MinWords {
		policy.MaxWords = policy.MinWords
	}
	// Arbitrary phrase windows are editorial fragments with a hard six-word
	// ceiling; complete sentence candidates are added separately below.
	if policy.MaxWords > 6 {
		policy.MaxWords = 6
	}
	if limit <= 0 {
		limit = policy.MaxResults
	}
	if limit <= 0 {
		limit = 3
	}
	if policy.MaxResults > 0 && limit > policy.MaxResults {
		limit = policy.MaxResults
	}

	tokens := tokenizeImportantPhrases(text)
	if len(tokens) < policy.MinWords {
		return nil
	}
	documentCounts := documentPhraseCounts(corpus, policy.MinWords, policy.MaxWords)
	candidates := make([]importantPhraseCandidate, 0, len(tokens))
	for start := range tokens {
		for wordCount := policy.MinWords; wordCount <= policy.MaxWords && start+wordCount <= len(tokens); wordCount++ {
			end := start + wordCount - 1
			if tokens[start].group != tokens[end].group {
				break
			}
			first, last := strings.ToLower(tokens[start].text), strings.ToLower(tokens[end].text)
			leadingFunctionWord := isImportantPhraseFunctionWord(first, profile)
			// Permit a leading article only at a sentence/clause boundary
			// ("O maior arrependimento..."); arbitrary windows beginning
			// with articles remain invalid.
			if isImportantPhraseFunctionWord(last, profile) ||
				(leadingFunctionWord && !startsAtPhraseBoundary(text, tokens[start].start)) {
				continue
			}
			contentWords, visualVerbs := 0, 0
			functionWords := 0
			allVisualVerbs := true
			for i := start; i <= end; i++ {
				word := strings.ToLower(tokens[i].text)
				if isImportantPhraseFunctionWord(word, profile) {
					functionWords++
					continue
				}
				if len([]rune(word)) < 3 {
					continue
				}
				contentWords++
				if isConfiguredVisualVerb(word, profile) {
					visualVerbs++
				} else {
					allVisualVerbs = false
				}
			}
			// Natural phrases may contain a small number of articles,
			// auxiliaries, prepositions or pronouns. A leading article is allowed
			// only at a phrase boundary; trailing function words remain invalid.
			// Reject candidates only when function words exceed one third total.
			if functionWords*3 > wordCount || contentWords < 2 || (policy.RejectVerbsWhenAll && allVisualVerbs) {
				continue
			}
			// A capitalised token after a comma/colon is usually a proper name
			// rather than the start of an editorial phrase (for example
			// "Tyson primero respiró"). Entity extraction may be disabled for a
			// phrase-only run, so keep that surface out deterministically here.
			if startsWithInteriorCapital(text, tokens[start].start) {
				continue
			}

			byteStart, byteEnd := tokens[start].start, tokens[end].end
			candidateText := text[byteStart:byteEnd]
			if ContainsProperNamePair(candidateText) {
				continue
			}
			runeStart := utf8.RuneCountInString(text[:byteStart])
			runeEnd := runeStart + utf8.RuneCountInString(candidateText)
			if overlapsAnyRuneSpan(runeStart, runeEnd, blockedSpans) {
				continue
			}

			// More grounded content words and configured action verbs increase
			// salience. A candidate that recurs across the document adds a
			// deterministic log-frequency term. Source order breaks the final
			// tie.
			score := contentWords*4 + visualVerbs*5 - wordCount
			if profile != nil {
				// With a lexicon, prefer complete three-to-six-word noun or
				// action chunks over tiny two-word fragments carrying the same
				// editorial signal. Without a profile, length must not be
				// rewarded blindly.
				score += wordCount * 2
			}
			score += documentTermBoost(documentCounts[normalizedPhraseKey(candidateText)])
			candidates = append(candidates, importantPhraseCandidate{
				text: candidateText, start: byteStart,
				tokenStart: start, tokenEnd: end, score: score,
			})
		}
	}
	// Natural complete sentences and neighboring sentence pairs are editorial
	// candidates outside the six-word cap used for arbitrary fragments. Their
	// token intervals are still disjoint-selected below, so one card cannot
	// step on words already assigned to an earlier card.
	sentences := sentenceTokenSpans(text, tokens)
	for i := range sentences {
		for width := 1; width <= 2 && i+width <= len(sentences); width++ {
			first, last := sentences[i], sentences[i+width-1]
			wordCount := last.tokenEnd - first.tokenStart + 1
			if width == 2 && wordCount <= policy.MaxWords {
				continue
			}
			span := importantPhraseCandidate{
				text:  strings.TrimSpace(text[first.byteStart:last.byteEnd]),
				start: first.byteStart, tokenStart: first.tokenStart, tokenEnd: last.tokenEnd,
				sentenceCount: width,
			}
			if !isTerminatedSentenceText(span.text) {
				continue
			}
			if sentenceCandidateAllowed(span, profile, blockedSpans, text) {
				span.score = phraseCandidateScore(span.text, profile, documentCounts)
				candidates = append(candidates, span)
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if candidates[i].sentenceCount != candidates[j].sentenceCount {
			return candidates[i].sentenceCount > candidates[j].sentenceCount
		}
		if candidates[i].start != candidates[j].start {
			return candidates[i].start < candidates[j].start
		}
		return candidates[i].tokenEnd < candidates[j].tokenEnd
	})

	selected := make([]importantPhraseCandidate, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, candidate := range candidates {
		key := strings.ToLower(strings.Join(strings.Fields(candidate.text), " "))
		if _, exists := seen[key]; exists {
			continue
		}
		overlaps := false
		for _, prior := range selected {
			if candidate.tokenStart <= prior.tokenEnd && prior.tokenStart <= candidate.tokenEnd {
				overlaps = true
				break
			}
		}
		if overlaps {
			continue
		}
		seen[key] = struct{}{}
		selected = append(selected, candidate)
		if len(selected) == limit {
			break
		}
	}

	out := make([]string, len(selected))
	for i, candidate := range selected {
		out[i] = candidate.text
	}
	return out
}

// ImportantWords returns the strongest words inside the SELECTED phrases, in
// phrase order. Words come from the phrases' exact surfaces, so a word overlay
// can never show a token the phrase overlay did not cover.
func ImportantWords(phrases []string, limit int, language string) []string {
	return ImportantWordsWithProfile(phrases, limit, importantPhraseLexicon(language))
}

// ImportantWordsWithProfile is ImportantWords against an explicit lexicon
// profile.
func ImportantWordsWithProfile(phrases []string, limit int, profile *linguistics.LexiconProfile) []string {
	if limit <= 0 {
		return nil
	}
	words := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, phrase := range phrases {
		for _, match := range importantPhraseWord.FindAllString(phrase, -1) {
			key := strings.ToLower(match)
			if len([]rune(key)) < 3 || isImportantPhraseFunctionWord(key, profile) {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			words = append(words, match)
			if len(words) == limit {
				return words
			}
		}
	}
	return words
}

// documentTermFrequencyWeight scales the log-frequency term. Keeping it an
// integer constant keeps the score integral and the ordering reproducible.
const documentTermFrequencyWeight = 4

// documentTermBoost turns a document-wide occurrence count into the score
// bonus k·log(1+occurrences). A candidate seen only once in the document adds
// nothing, so Select (no corpus) and SelectWithCorpus agree on single-occurrence
// surfaces; recurrence is the signal.
func documentTermBoost(occurrences int) int {
	if occurrences < 2 {
		return 0
	}
	return int(math.Round(documentTermFrequencyWeight * math.Log1p(float64(occurrences))))
}

// normalizedPhraseKey is the case- and whitespace-insensitive identity used for
// document-frequency counting. It matches the key the selection dedupe uses, so
// a candidate and its corpus occurrences resolve to the same bucket.
func normalizedPhraseKey(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

// documentPhraseCounts counts how many times each [minWords,maxWords] word
// window (within a phrase-boundary group) occurs across every corpus text. It
// enumerates windows with the same tokenizer and boundaries as Select, so a
// candidate's key is only counted when the document contains that exact
// surface.
func documentPhraseCounts(corpus []string, minWords, maxWords int) map[string]int {
	if len(corpus) == 0 {
		return nil
	}
	counts := make(map[string]int)
	for _, document := range corpus {
		tokens := tokenizeImportantPhrases(document)
		for start := range tokens {
			for wordCount := minWords; wordCount <= maxWords && start+wordCount <= len(tokens); wordCount++ {
				end := start + wordCount - 1
				if tokens[start].group != tokens[end].group {
					break
				}
				counts[normalizedPhraseKey(document[tokens[start].start:tokens[end].end])]++
			}
		}
	}
	return counts
}

func importantPhraseLexicon(language string) *linguistics.LexiconProfile {
	registry := linguistics.DefaultLexiconOrNil()
	if registry == nil {
		return nil
	}
	if registry.HasProfile(language) {
		return registry.Resolve(language)
	}
	if registry.HasProfile("fallback") {
		return registry.Resolve("fallback")
	}
	return nil
}

func tokenizeImportantPhrases(text string) []importantPhraseToken {
	indexes := importantPhraseWord.FindAllStringIndex(text, -1)
	tokens := make([]importantPhraseToken, 0, len(indexes))
	group := 0
	for i, index := range indexes {
		if i > 0 && phraseBoundaryBetween(text[indexes[i-1][1]:index[0]]) {
			group++
		}
		tokens = append(tokens, importantPhraseToken{
			text: text[index[0]:index[1]], start: index[0], end: index[1], group: group,
		})
	}
	return tokens
}

func phraseBoundaryBetween(gap string) bool {
	return strings.ContainsAny(gap, ".!?;,:—–\n\r")
}

func sentenceBoundaryBetween(gap string) bool {
	return strings.ContainsAny(gap, ".!?\n\r")
}

func sentenceTokenSpans(text string, tokens []importantPhraseToken) []importantPhraseSentence {
	if len(tokens) == 0 {
		return nil
	}
	var sentences []importantPhraseSentence
	start := 0
	for next := 1; next < len(tokens); next++ {
		gapStart := tokens[next-1].end
		gap := text[gapStart:tokens[next].start]
		if !sentenceBoundaryBetween(gap) {
			continue
		}
		sentences = append(sentences, importantPhraseSentence{
			byteStart: tokens[start].start, byteEnd: sentenceBoundaryEndByte(gap, gapStart),
			tokenStart: start, tokenEnd: next - 1,
		})
		start = next
	}
	endByte := len(text)
	for endByte > tokens[len(tokens)-1].end {
		r, size := utf8.DecodeLastRuneInString(text[:endByte])
		if !unicode.IsSpace(r) {
			break
		}
		endByte -= size
	}
	sentences = append(sentences, importantPhraseSentence{
		byteStart: tokens[start].start, byteEnd: endByte,
		tokenStart: start, tokenEnd: len(tokens) - 1,
	})
	return sentences
}

// sentenceBoundaryEndByte includes punctuation and closing quotes, but not
// whitespace before the next sentence.
func sentenceBoundaryEndByte(gap string, absoluteGapStart int) int {
	for offset, r := range gap {
		if !strings.ContainsRune(".!?\n\r", r) {
			continue
		}
		end := offset + utf8.RuneLen(r)
		for end < len(gap) {
			next, size := utf8.DecodeRuneInString(gap[end:])
			if strings.ContainsRune(`"'’”)]}»`, next) {
				end += size
				continue
			}
			break
		}
		return absoluteGapStart + end
	}
	return absoluteGapStart + len(gap)
}

func isTerminatedSentenceText(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for len(value) > 0 {
		r, size := utf8.DecodeLastRuneInString(value)
		if strings.ContainsRune(`"'’”)]}»`, r) {
			value = strings.TrimSpace(value[:len(value)-size])
			continue
		}
		break
	}
	if value == "" {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(value)
	return r == '.' || r == '!' || r == '?'
}

func sentenceCandidateAllowed(candidate importantPhraseCandidate, profile *linguistics.LexiconProfile, blockedSpans [][2]int, text string) bool {
	wordCount := candidate.tokenEnd - candidate.tokenStart + 1
	if wordCount < 2 || strings.TrimSpace(candidate.text) == "" || ContainsProperNamePair(candidate.text) {
		return false
	}
	tokens := importantPhraseWord.FindAllString(candidate.text, -1)
	if len(tokens) == 0 {
		return false
	}
	first := strings.ToLower(tokens[0])
	last := strings.ToLower(tokens[len(tokens)-1])
	leadingFunctionWord := isImportantPhraseFunctionWord(first, profile)
	if isImportantPhraseFunctionWord(last, profile) || (leadingFunctionWord && !startsAtPhraseBoundary(text, candidate.start)) {
		return false
	}
	contentWords, visualVerbs, functionWords := 0, 0, 0
	allVisualVerbs := true
	for _, token := range tokens {
		word := strings.ToLower(token)
		if isImportantPhraseFunctionWord(word, profile) {
			functionWords++
			continue
		}
		if len([]rune(word)) < 3 {
			continue
		}
		contentWords++
		if isConfiguredVisualVerb(word, profile) {
			visualVerbs++
		} else {
			allVisualVerbs = false
		}
	}
	_ = visualVerbs
	if functionWords*3 > wordCount || contentWords < 2 {
		return false
	}
	// Reject verb-only surfaces when the profile opts in.
	policy := linguistics.DefaultPhraseExtractionPolicy()
	if profile != nil {
		policy = profile.PhrasePolicy
	}
	if policy.RejectVerbsWhenAll && allVisualVerbs && contentWords > 0 {
		return false
	}
	if startsWithInteriorCapital(text, candidate.start) {
		return false
	}
	runeStart := utf8.RuneCountInString(text[:candidate.start])
	runeEnd := runeStart + utf8.RuneCountInString(candidate.text)
	return !overlapsAnyRuneSpan(runeStart, runeEnd, blockedSpans)
}

func phraseCandidateScore(text string, profile *linguistics.LexiconProfile, documentCounts map[string]int) int {
	contentWords, visualVerbs := 0, 0
	for _, token := range importantPhraseWord.FindAllString(text, -1) {
		word := strings.ToLower(token)
		if isImportantPhraseFunctionWord(word, profile) || len([]rune(word)) < 3 {
			continue
		}
		contentWords++
		if isConfiguredVisualVerb(word, profile) {
			visualVerbs++
		}
	}
	wordCount := len(importantPhraseWord.FindAllString(text, -1))
	score := contentWords*4 + visualVerbs*5 - wordCount
	if profile != nil {
		score += wordCount * 2
	}
	normalized := normalizedPhraseKey(text)
	trimmed := strings.Trim(normalized, ".,!?;:\"'’”)]}» ")
	if trimmed == "" {
		trimmed = normalized
	}
	boost := documentTermBoost(documentCounts[normalized])
	if trimmed != normalized {
		if tb := documentTermBoost(documentCounts[trimmed]); tb > boost {
			boost = tb
		}
	}
	score += boost
	return score
}

func startsAtPhraseBoundary(text string, byteStart int) bool {
	if byteStart <= 0 || byteStart > len(text) {
		return true
	}
	prefix := strings.TrimRightFunc(text[:byteStart], unicode.IsSpace)
	if prefix == "" {
		return true
	}
	last, _ := utf8.DecodeLastRuneInString(prefix)
	return strings.ContainsRune(".!?;,:—–\n\r", last)
}

func startsWithInteriorCapital(text string, byteStart int) bool {
	if byteStart <= 0 || byteStart > len(text) {
		return false
	}
	previous := strings.TrimSpace(text[:byteStart])
	if previous == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(previous)
	return last == ','
}

func isImportantPhraseFunctionWord(word string, profile *linguistics.LexiconProfile) bool {
	if profile == nil {
		return false
	}
	_, stop := profile.StopWords[word]
	_, function := profile.FunctionWords[word]
	return stop || function
}

func isConfiguredVisualVerb(word string, profile *linguistics.LexiconProfile) bool {
	if profile == nil {
		return false
	}
	_, ok := profile.VisualVerbs[word]
	return ok
}

// ContainsProperNamePair reports whether value carries two consecutive
// capitalised words. The selector is the single owner of this rule: it keeps a
// phrase surface free of proper-name runs even when the entity extractor missed
// a name, validating candidates without rewriting them.
//
// A word that is ENTIRELY uppercase with two or more letters is an acronym
// (USA, NATO, AI), not a capitalised name word: it neither starts nor continues
// a proper-name run. Without this, adjacent acronyms ("USA NATO") or a shouted
// sentence would be rejected as if they carried a person's name.
func ContainsProperNamePair(value string) bool {
	previousTitle := false
	// FieldsSeq iterates without materialising the []string that
	// strings.Fields would allocate for every candidate phrase.
	for raw := range strings.FieldsSeq(value) {
		word := strings.Trim(raw, ".,;:!?\"'’()[]{}")
		currentTitle := false
		if !isAcronymToken(word) {
			for _, r := range word {
				currentTitle = unicode.IsUpper(r)
				break
			}
		}
		if currentTitle && previousTitle {
			return true
		}
		previousTitle = currentTitle
	}
	return false
}

// isAcronymToken reports whether word is an all-uppercase token of two or more
// letters (USA, NATO, AI). A single capital ("I", "A") is an ordinary
// capitalised word, not an acronym, so it keeps its previous meaning.
func isAcronymToken(word string) bool {
	letters := 0
	for _, r := range word {
		if !unicode.IsLetter(r) {
			continue
		}
		if !unicode.IsUpper(r) {
			return false
		}
		letters++
	}
	return letters >= 2
}

func overlapsAnyRuneSpan(start, end int, spans [][2]int) bool {
	for _, span := range spans {
		if start < span[1] && span[0] < end {
			return true
		}
	}
	return false
}
