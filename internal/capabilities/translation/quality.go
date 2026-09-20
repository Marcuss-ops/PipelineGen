// Package translation — quality.go: pure quality assessment of a translation.
//
// WHY: the Argos primary is fast but not always right. Observed in the
// multilingual rehearsal: a Turkish target lost a whole clause and kept an
// English subject, Indonesian came back ungrammatical, "reliable rendering"
// became "interpretación fiable" (interpretation) in Spanish. Every one of
// those answers was non-empty, so the pre-existing chain (which only falls back
// on an error or an empty string) accepted and certified them.
//
// AssessTranslation is the deterministic gate that closes that hole. It flags
// only DEGENERATE answers, never merely imperfect ones: a truncation, a
// runaway repetition, a copy of the source, or a text that is not in the
// target language. Thresholds are deliberately generous so that a legitimate
// reordering or a verbose target language (en→de expands, it shrinks) is never
// rejected.
//
// The assessment is pure: same inputs, same verdict, no I/O.
package translation

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// QualityIssue is one detected degeneracy of a translation.
type QualityIssue string

const (
	// IssueEmpty means the provider answered with no usable text.
	IssueEmpty QualityIssue = "empty"
	// IssueSourceLeak means the "translation" is byte-identical to the source.
	IssueSourceLeak QualityIssue = "source_leak"
	// IssueTruncated means the answer is far too short to be a full rendering
	// of the source (the translator dropped content).
	IssueTruncated QualityIssue = "truncated"
	// IssueRunaway means the answer is far too long (a repetition loop).
	IssueRunaway QualityIssue = "runaway"
	// IssueRepetition means the answer repeats itself, a classic degenerate
	// generation.
	IssueRepetition QualityIssue = "repetition"
	// IssueWrongLanguage means the answer is not written in the target
	// language (see VerifyTargetLanguage).
	IssueWrongLanguage QualityIssue = "wrong_language"
)

// Assessment is the result of AssessTranslation. OK is true when the answer
// carries no degeneracy; Issues carries one entry per detected problem.
type Assessment struct {
	OK          bool
	Issues      []QualityIssue
	Reason      string
	LengthRatio float64
}

// Acceptable reports whether the translation may be persisted and burned.
func (a Assessment) Acceptable() bool { return a.OK }

// minRunesForLengthCheck is the shortest source text that makes the
// truncation/runaway verdict meaningful. Below it a legitimately terse
// translation (a two-word caption) would trip the ratio.
const minRunesForLengthCheck = 60

// truncationFloor and runawayCeiling bound the accepted output/source length
// ratio. A real translation rarely drops below 40% (Italian is terse, German
// verbose) or grows past 350% of the source.
const (
	truncationFloor = 0.40
	runawayCeiling  = 3.50
)

// minWordsForRepetition is the smallest word count that makes the repetition
// verdict meaningful.
const minWordsForRepetition = 14

// uniqueWordFloor is the smallest accepted unique/total word ratio. Natural
// prose keeps well above 0.25; a repetition loop falls far below.
const uniqueWordFloor = 0.12

// AssessTranslation evaluates a translated answer against its source.
//
// sourceLang/targetLang are the request's BCP-47 tags; the language and
// source-leak checks are skipped when they name the same language (a request to
// "translate" en→en is a no-op, not a leak).
func AssessTranslation(source, translated, sourceLang, targetLang string) Assessment {
	trimmed := strings.TrimSpace(translated)
	if trimmed == "" {
		return Assessment{OK: false, Issues: []QualityIssue{IssueEmpty}, Reason: "empty translation"}
	}

	sameLang := sameBaseLanguage(sourceLang, targetLang)
	var issues []QualityIssue
	var reasons []string

	srcRunes := utf8.RuneCountInString(strings.TrimSpace(source))
	outRunes := utf8.RuneCountInString(trimmed)
	ratio := 0.0
	if srcRunes > 0 {
		ratio = float64(outRunes) / float64(srcRunes)
	}

	if !sameLang && sourceLang != "" && srcRunes >= 3 && normalizeForComparison(source) == normalizeForComparison(trimmed) {
		issues = append(issues, IssueSourceLeak)
		reasons = append(reasons, "output equals the source text")
	}

	if srcRunes >= minRunesForLengthCheck {
		if ratio < truncationFloor {
			issues = append(issues, IssueTruncated)
			reasons = append(reasons, fmt.Sprintf("output is %.0f%% of the source length", ratio*100))
		} else if ratio > runawayCeiling {
			issues = append(issues, IssueRunaway)
			reasons = append(reasons, fmt.Sprintf("output is %.0f%% of the source length", ratio*100))
		}
	}

	if hasRepetitionLoop(trimmed) {
		issues = append(issues, IssueRepetition)
		reasons = append(reasons, "output repeats itself")
	}

	if !sameLang {
		if ok, reason := VerifyTargetLanguage(trimmed, targetLang); !ok {
			issues = append(issues, IssueWrongLanguage)
			reasons = append(reasons, reason)
		}
	}

	return Assessment{
		OK:          len(issues) == 0,
		Issues:      issues,
		Reason:      strings.Join(reasons, "; "),
		LengthRatio: ratio,
	}
}

// ErrDegenerateTranslation is returned when neither the primary nor the
// fallback provider produced an acceptable translation. It is a typed error so
// callers can fail the language loudly instead of persisting a wrong subtitle
// under a real translation key (godlike/07).
type ErrDegenerateTranslation struct {
	SourceLang string
	TargetLang string
	Provider   string
	Issues     []QualityIssue
	Reason     string
}

func (e *ErrDegenerateTranslation) Error() string {
	if e == nil {
		return "translation: degenerate translation"
	}
	return fmt.Sprintf("translation: degenerate %s→%s translation from %s (%s)", e.SourceLang, e.TargetLang, e.Provider, e.Reason)
}

// sameBaseLanguage reports whether two BCP-47 tags name the same language.
func sameBaseLanguage(a, b string) bool {
	ba, bb := baseLanguage(a), baseLanguage(b)
	return ba != "" && ba == bb
}

// normalizeForComparison lowercases a text, collapses whitespace and drops
// trailing punctuation, so a "translation" that only re-cased or re-spaced its
// source is still detected as a leak.
func normalizeForComparison(text string) string {
	fields := strings.Fields(strings.ToLower(text))
	joined := strings.Join(fields, " ")
	return strings.TrimRight(joined, " .,;:!?…")
}

// hasRepetitionLoop reports whether a text repeats itself to a degenerate
// degree: either the same sentence three or more times, or a vocabulary so
// limited that it cannot be prose.
func hasRepetitionLoop(text string) bool {
	words := tokenizeWords(text)
	if len(words) < minWordsForRepetition {
		return false
	}
	unique := make(map[string]struct{}, len(words))
	for _, w := range words {
		unique[w] = struct{}{}
	}
	if float64(len(unique))/float64(len(words)) < uniqueWordFloor {
		return true
	}
	counts := make(map[string]int)
	for _, sentence := range splitSentences(text) {
		if len(strings.Fields(sentence)) < 4 {
			continue
		}
		key := strings.Join(strings.Fields(strings.ToLower(sentence)), " ")
		counts[key]++
		if counts[key] >= 3 {
			return true
		}
	}
	return false
}

// splitSentences splits a text on the terminal punctuation of a sentence.
func splitSentences(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	})
}
