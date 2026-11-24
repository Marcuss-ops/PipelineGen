// Package phrases owns the phrase-selection wire contract and the pure
// projections over it: the request the Rust selector receives, the profile it
// reads, and the Go-side grounding of the spans it returns.
//
// The selector decision stays in Rust. Nothing here performs I/O, so both the
// scripts root and the vidrush package depend on this leaf instead of on each
// other.
package phrases

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// PhraseSelectionProfile is the lexicon data the Rust selector needs. The
// selector decision stays in Rust; Go only loads and forwards the profile.
type PhraseSelectionProfile struct {
	StopWords          []string `json:"stop_words,omitempty"`
	FunctionWords      []string `json:"function_words,omitempty"`
	VisualVerbs        []string `json:"visual_verbs,omitempty"`
	MinWords           int      `json:"min_words,omitempty"`
	MaxWords           int      `json:"max_words,omitempty"`
	MaxResults         int      `json:"max_results,omitempty"`
	RejectVerbsWhenAll bool     `json:"reject_verbs_when_all"`
}

// PhraseSelectionRequest asks the Rust worker for source-grounded phrases in
// editorial order. BlockedRuneSpans are the entity ranges the caller already
// grounded; the selector may not overlap them.
type PhraseSelectionRequest struct {
	Text             string
	BlockedRuneSpans [][2]int
	Limit            int
	MinWords         int
	MaxWords         int
	Corpus           []string
	Profile          *PhraseSelectionProfile
}

// SelectedPhrase is one phrase returned by the Rust selector. Byte spans index
// the trimmed request text; rune spans are the same coordinates Go annotations
// use.
type SelectedPhrase struct {
	Text      string `json:"text"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	StartRune int    `json:"start_rune"`
	EndRune   int    `json:"end_rune"`
	Score     int64  `json:"score"`
}

// PhraseSelector is the single production seam for phrase selection. The
// semantic-engine adapter implements it over the Rust worker.
type PhraseSelector interface {
	SelectPhrases(ctx context.Context, request PhraseSelectionRequest) ([]SelectedPhrase, error)
}

// NewPhraseSelectionProfile projects a loaded lexicon profile onto the wire
// contract. Keys are sorted so the request is deterministic across map
// iteration.
func NewPhraseSelectionProfile(profile *linguistics.LexiconProfile) *PhraseSelectionProfile {
	if profile == nil {
		return nil
	}
	return &PhraseSelectionProfile{
		StopWords:          sortedWordSet(profile.StopWords),
		FunctionWords:      sortedWordSet(profile.FunctionWords),
		VisualVerbs:        sortedWordSet(profile.VisualVerbs),
		MinWords:           profile.PhrasePolicy.MinWords,
		MaxWords:           profile.PhrasePolicy.MaxWords,
		MaxResults:         profile.PhrasePolicy.MaxResults,
		RejectVerbsWhenAll: profile.PhrasePolicy.RejectVerbsWhenAll,
	}
}

// ProfileForLanguage loads the same lexicon profile the legacy
// Go selector used. The decision still happens in Rust; this only projects the
// configured stop/function words, visual verbs and phrase policy onto the
// wire.
func ProfileForLanguage(language string) *PhraseSelectionProfile {
	registry := linguistics.DefaultProfilesOrNil()
	if registry == nil {
		return nil
	}
	if registry.HasProfile(language) {
		return NewPhraseSelectionProfile(registry.Resolve(language))
	}
	if registry.HasProfile("fallback") {
		return NewPhraseSelectionProfile(registry.Resolve("fallback"))
	}
	return nil
}

var ImportantPhraseWord = regexp.MustCompile(`[\p{L}\p{M}]+(?:['’][\p{L}\p{M}]+)?`)

// ImportantWordsFromPhrases projects the strongest words inside the selected
// phrases, in phrase order. It is a projection of already-selected Rust spans,
// not a second phrase selector.
func ImportantWordsFromPhrases(phrases []string, limit int, profile *PhraseSelectionProfile) []string {
	if limit <= 0 {
		return nil
	}
	functionWords := make(map[string]struct{}, len(profileFunctionWords(profile)))
	if profile != nil {
		for _, word := range profile.FunctionWords {
			functionWords[strings.ToLower(word)] = struct{}{}
		}
	}
	words := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, phrase := range phrases {
		for _, match := range ImportantPhraseWord.FindAllString(phrase, -1) {
			key := strings.ToLower(match)
			if len([]rune(key)) < 3 {
				continue
			}
			if _, blocked := functionWords[key]; blocked {
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

func profileFunctionWords(profile *PhraseSelectionProfile) []string {
	if profile == nil {
		return nil
	}
	return profile.FunctionWords
}

// ContainsProperNamePair is the ingress guard for operator-supplied phrase
// hints. Selector-derived candidates are guarded in Rust.
func ContainsProperNamePair(value string) bool {
	previousTitle := false
	for _, raw := range strings.Fields(value) {
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

// CompleteGroundedPhrase completes a selected phrase to the sentence that
// contains it: it snaps the start back to the previous sentence boundary and
// extends the end over terminal punctuation when the selector returned the
// sentence without it.
//
// The selector returns editorial noun phrases from inside a sentence by
// design, so requiring the selected span itself to start at a boundary
// discarded every candidate whose sentence-opening token was not part of the
// phrase. English selections are mid-sentence much more often than Italian
// ones, which silently removed the whole phrase surface from English scenes.
func CompleteGroundedPhrase(text string, span scriptpkg.AnnotationSpan) (scriptpkg.AnnotationSpan, bool) {
	runes := []rune(text)
	start, end := span.StartRune, span.EndRune
	if start < 0 || end <= start || end > len(runes) {
		return scriptpkg.AnnotationSpan{}, false
	}
	for i := start - 1; i >= 0; i-- {
		if isGroundingSentenceBoundary(runes[i]) {
			start = i + 1
			break
		}
		start = i
	}
	for start < end && (unicode.IsSpace(runes[start]) || isSentenceOpeningPunctuation(runes[start])) {
		start++
	}
	if start >= end {
		return scriptpkg.AnnotationSpan{}, false
	}
	for end > start && unicode.IsSpace(runes[end-1]) {
		end--
	}
	// Extend the end to the terminal punctuation of the containing sentence.
	// A fragment selected from the middle of a sentence has words after it, so
	// the old "the next non-space rune must be punctuation" rule rejected the
	// candidate instead of completing it.
	if !isGroundingSentenceBoundary(runes[end-1]) {
		sentenceEnd := -1
		for i := end; i < len(runes); i++ {
			if isGroundingSentenceBoundary(runes[i]) {
				sentenceEnd = i + 1
				break
			}
		}
		if sentenceEnd < 0 {
			return scriptpkg.AnnotationSpan{}, false
		}
		end = sentenceEnd
	}
	span.Text = strings.TrimSpace(string(runes[start:end]))
	span.EndRune = end
	return span, true
}

func isGroundingSentenceBoundary(r rune) bool {
	switch r {
	case '.', '?', '!', ';', ':':
		return true
	default:
		return false
	}
}

// isSentenceOpeningPunctuation is the padding between a sentence boundary and
// its first word: quotes and brackets around a sentence, never an apostrophe
// that belongs to the first word.
func isSentenceOpeningPunctuation(r rune) bool {
	switch r {
	case '"', '\u201c', '\u201d', '\u2018', '\u2019', '\u00ab', '\u00bb', '(', '[', '\u2014', '\u2013':
		return true
	default:
		return false
	}
}

func sortedWordSet(words map[string]struct{}) []string {
	if len(words) == 0 {
		return nil
	}
	out := make([]string, 0, len(words))
	for word := range words {
		if word = strings.TrimSpace(word); word != "" {
			out = append(out, word)
		}
	}
	sort.Strings(out)
	return out
}
