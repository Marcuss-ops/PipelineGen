// selection_sentences.go — sentence segmentation and candidate scoring, split out of selection.go (600-line file gate).
package phrases

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

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
