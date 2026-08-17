package audio

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// LocatedPhrase is one deterministic match of a phrase inside the canonical
// word-level timing. WordStart/WordEnd reference the artifact word indices;
// StartUS/EndUS span from the first matched word's start to the last
// matched word's end, so the caller gets exact microsecond anchors without
// any invented interpolation.
type LocatedPhrase struct {
	Text       string `json:"text"`
	Occurrence int    `json:"occurrence"`

	WordStart int `json:"word_start"`
	WordEnd   int `json:"word_end"`

	StartUS int64 `json:"start_us"`
	EndUS   int64 `json:"end_us"`
}

// ErrPhraseNotFound is returned when the phrase does not occur in the
// artifact as an exact contiguous token match. No fuzzy matching is ever
// attempted: a missing phrase fails explicitly rather than producing a
// plausible-but-wrong timestamp.
var ErrPhraseNotFound = errors.New("phrase not found in speech timing")

// apostropheRunes folds the typographic apostrophe variants to the ASCII
// apostrophe so "l'Italia" and "l’Italia" compare equal.
var apostropheRunes = map[rune]rune{
	'\u2018': '\'', // left single quotation mark
	'\u2019': '\'', // right single quotation mark
	'\u201B': '\'', // single high-reversed-9 quotation mark
	'\u02BC': '\'', // modifier letter apostrophe
}

// LocatePhrase finds every exact contiguous occurrence of phrase inside the
// canonical word timing. Matching is deterministic and normalization-only:
// Unicode NFC, case folding, apostrophe folding, punctuation stripped around
// tokens, whitespace collapsed. Occurrences are numbered 1..n in document
// order (non-overlapping: after a match, scanning resumes after the match).
//
// The artifact is validated fail-closed first: an invalid timing cannot
// produce timestamps. An empty phrase, an empty artifact, or a phrase that
// does not occur returns ErrPhraseNotFound.
func LocatePhrase(timing SpeechTimingArtifact, phrase string) ([]LocatedPhrase, error) {
	if err := timing.Validate(); err != nil {
		return nil, err
	}
	query := phraseTokens(phrase)
	if len(query) == 0 {
		return nil, ErrPhraseNotFound
	}

	// Normalize the boundary words with the SAME dash-splitting the phrase
	// side uses, so dash-joined script words align with the separate word
	// boundaries TTS reports for them. sourceWord maps each flattened token
	// back to its timing.Words index so WordStart/WordEnd stay valid.
	normalized := make([]string, 0, len(timing.Words))
	sourceWord := make([]int, 0, len(timing.Words))
	for wi, word := range timing.Words {
		for _, token := range phraseFragmentTokens(word.Text) {
			normalized = append(normalized, token)
			sourceWord = append(sourceWord, wi)
		}
	}
	if len(query) > len(normalized) {
		return nil, fmt.Errorf("%w: phrase has %d tokens but timing has %d word tokens", ErrPhraseNotFound, len(query), len(normalized))
	}

	var found []LocatedPhrase
	for start := 0; start+len(query) <= len(normalized); start++ {
		match := true
		for j, token := range query {
			if normalized[start+j] != token {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		first := timing.Words[sourceWord[start]]
		last := timing.Words[sourceWord[start+len(query)-1]]
		found = append(found, LocatedPhrase{
			Text:       strings.TrimSpace(phrase),
			Occurrence: len(found) + 1,
			WordStart:  first.Index,
			WordEnd:    last.Index,
			StartUS:    first.StartUS,
			EndUS:      last.EndUS,
		})
		// Non-overlapping: continue scanning right after the match.
		start += len(query) - 1
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrPhraseNotFound, describePhraseMismatch(query, normalized))
	}
	return found, nil
}

// describePhraseMismatch returns a bounded diagnostic describing WHERE a
// phrase fails to match the word timing when aligned at token 0 (the full
// scene narration must align from the first spoken word). It reports the
// first divergent token index plus the surrounding token window — never the
// full arrays (the output stays bounded even for a 200+ word scene).
func describePhraseMismatch(query, normalized []string) string {
	limit := len(query)
	if len(normalized) < limit {
		limit = len(normalized)
	}
	for i := 0; i < limit; i++ {
		if query[i] == normalized[i] {
			continue
		}
		qLo := i - 2
		if qLo < 0 {
			qLo = 0
		}
		qHi := i + 3
		if qHi > len(query) {
			qHi = len(query)
		}
		nHi := i + 3
		if nHi > len(normalized) {
			nHi = len(normalized)
		}
		return fmt.Sprintf("first mismatch at token %d: phrase[%s] vs timing[%s]", i, strings.Join(query[qLo:qHi], " "), strings.Join(normalized[qLo:nHi], " "))
	}
	return fmt.Sprintf("query=%d tokens, timing=%d tokens (shared prefix, no full alignment)", len(query), len(normalized))
}

// phraseTokens splits a phrase into its matching tokens: whitespace
// collapsed, dash-joined fragments split into separate tokens (TTS reports
// em/en-dash-joined words as separate word boundaries), each fragment
// normalized, punctuation-only fragments dropped.
func phraseTokens(phrase string) []string {
	fields := strings.FieldsFunc(phrase, unicode.IsSpace)
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		tokens = append(tokens, phraseFragmentTokens(field)...)
	}
	return tokens
}

// phraseFragmentTokens normalizes one field into its matching tokens.
// Both whitespace AND dash characters act as token separators: Edge TTS
// reports dash-joined words as separate word boundaries
// ("interpretation—in" → "interpretation" + "in") AND occasionally emits a
// single WordBoundary chunk that contains several space-separated words
// (e.g. "corso degli anni"). The boundary side is normalized through the
// SAME helper as the phrase side so both align regardless of how Edge
// grouped the words.
func phraseFragmentTokens(field string) []string {
	var out []string
	for _, fragment := range strings.FieldsFunc(field, func(r rune) bool {
		return unicode.IsSpace(r) || isDashSeparator(r)
	}) {
		if token := normalizePhraseToken(fragment); token != "" {
			out = append(out, token)
		}
	}
	return out
}

// isDashSeparator reports whether r is a dash character used to separate
// words: hyphen-minus, hyphen, non-breaking hyphen, figure dash, en-dash,
// em-dash, horizontal bar, and minus sign.
func isDashSeparator(r rune) bool {
	switch r {
	case '-', '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
		return true
	}
	return false
}

// normalizePhraseToken applies the canonical matching normalization:
// Unicode NFC (composed vs decomposed accents compare equal), lowercase
// case folding, apostrophe variants folded to ASCII ', and punctuation
// stripped from the token edges (never the apostrophe — contractions like
// "l'Italia" keep it internal).
func normalizePhraseToken(token string) string {
	token = norm.NFC.String(token)
	token = strings.ToLower(token)
	var b strings.Builder
	b.Grow(len(token))
	for _, r := range token {
		if folded, ok := apostropheRunes[r]; ok {
			r = folded
		}
		b.WriteRune(r)
	}
	token = b.String()
	return strings.TrimFunc(token, func(r rune) bool {
		return r != '\'' && unicode.IsPunct(r)
	})
}
