package audio

import (
	"errors"
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
	if len(query) == 0 || len(timing.Words) < len(query) {
		return nil, ErrPhraseNotFound
	}
	normalized := make([]string, len(timing.Words))
	for i, word := range timing.Words {
		normalized[i] = normalizePhraseToken(word.Text)
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
		first := timing.Words[start]
		last := timing.Words[start+len(query)-1]
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
		return nil, ErrPhraseNotFound
	}
	return found, nil
}

// phraseTokens splits a phrase into its matching tokens: whitespace
// collapsed, each field normalized, punctuation-only fields dropped.
func phraseTokens(phrase string) []string {
	fields := strings.FieldsFunc(phrase, unicode.IsSpace)
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if token := normalizePhraseToken(field); token != "" {
			tokens = append(tokens, token)
		}
	}
	return tokens
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
