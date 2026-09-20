// Package translation — langverify.go: language/script verification of a
// translated string.
//
// WHY: the Argos primary is deterministic but it can answer in the WRONG
// language — the target package is missing, the model falls back to the source,
// or a short/technical fragment is copied verbatim. A non-empty answer is not
// proof of a translation, so the quality gate (quality.go) needs a verifier
// that says "this text is not plausibly <target>".
//
// Two independent signals, from strongest to weakest:
//
//  1. Script check (confident, self-contained). For a target written in a
//     non-Latin script (Russian → Cyrillic, Japanese → Han/Kana, Korean →
//     Hangul, Arabic, Hebrew, Greek, Thai, Devanagari) a translation with ZERO
//     letters of the expected script is not that language — full stop.
//  2. Function-word check (heuristic). For the Latin-script targets the script
//     cannot distinguish languages, so the text is scored against the
//     per-language function-word sets of the canonical LexiconRegistry. It only
//     fails when the text carries NO function word of the target AND clearly
//     matches another supported language. A technical/vocabulary fragment with
//     no function words at all stays accepted (no false positive on
//     "rendering pipeline 4K").
//
// godlike/06 SSOT: this file declares NO linguistic data. The word sets come
// from linguistics.DefaultLexicon() (config/lexicons/<lang>/*.txt), the single
// owner of per-language data in this repository. When the composition root has
// not installed the registry (isolated tests), the function-word check abstains
// rather than manufacturing data; DefaultLexicon() would panic, so the
// non-failing DefaultLexiconOrNil() is used deliberately.
package translation

import (
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// scriptClass is the writing-system family of a rune. Only the families the
// pipeline can see matter; everything else is scriptOther.
type scriptClass int

const (
	scriptOther scriptClass = iota
	scriptLatin
	scriptCyrillic
	scriptGreek
	scriptHan
	scriptKana
	scriptHangul
	scriptArabic
	scriptHebrew
	scriptDevanagari
	scriptThai
)

// minLettersForScriptCheck is the smallest number of letters that makes the
// script verdict meaningful. A three-letter cue ("OK") is never judged.
const minLettersForScriptCheck = 6

// minWordsForStopwords is the smallest word count that makes the function-word
// verdict meaningful. Below it the check abstains.
const minWordsForStopwords = 8

// minForeignStopwordHits is how many function words another language must match
// before the text is declared to be that other language.
const minForeignStopwordHits = 3

// lexicallyComparableLanguages is the fixed iteration order of the
// function-word comparison, so the verdict is deterministic even when two
// languages tie. It lists LANGUAGE CODES only — every word set is resolved from
// the LexiconRegistry, never hardcoded here.
var lexicallyComparableLanguages = []string{"en", "it", "es", "de", "fr", "pt", "nl", "pl", "ru", "tr", "id"}

// VerifyTargetLanguage reports whether text is plausibly written in lang. The
// returned reason is a short operator-facing explanation and is empty when the
// verdict is positive or the check abstains.
func VerifyTargetLanguage(text, lang string) (bool, string) {
	base := baseLanguage(lang)
	if base == "" || strings.TrimSpace(text) == "" {
		return true, ""
	}
	counts := scriptCounts(text)
	if totalLetters(counts) == 0 {
		return true, "" // digits/symbols only: nothing to verify
	}
	expected := expectedScripts(base)
	if len(expected) > 0 && totalLetters(counts) >= minLettersForScriptCheck {
		matched := 0
		for _, class := range expected {
			matched += counts[class]
		}
		if matched == 0 {
			return false, "text carries no " + scriptName(expected[0]) + " characters expected for " + base
		}
	}
	if expected[0] == scriptLatin {
		if mismatch, reason := stopwordMismatch(text, base); mismatch {
			return false, reason
		}
	}
	return true, ""
}

// baseLanguage returns the lowercase primary subtag of a BCP-47 tag ("pt-BR"
// → "pt"). An unreadable tag yields "".
func baseLanguage(tag string) string {
	tag = strings.TrimSpace(strings.ToLower(tag))
	if tag == "" {
		return ""
	}
	if i := strings.IndexAny(tag, "-_"); i >= 0 {
		return tag[:i]
	}
	return tag
}

// expectedScripts maps a base language to the writing systems a text in that
// language must contain. Latin-script targets return [scriptLatin]; the script
// check passes for them and the function-word check does the real work.
func expectedScripts(base string) []scriptClass {
	switch base {
	case "ru", "uk", "bg", "sr", "mk", "be", "kk":
		return []scriptClass{scriptCyrillic}
	case "ja":
		return []scriptClass{scriptHan, scriptKana}
	case "zh", "yue":
		return []scriptClass{scriptHan}
	case "ko":
		return []scriptClass{scriptHangul}
	case "ar", "fa", "ur":
		return []scriptClass{scriptArabic}
	case "he", "yi":
		return []scriptClass{scriptHebrew}
	case "el":
		return []scriptClass{scriptGreek}
	case "th":
		return []scriptClass{scriptThai}
	case "hi", "mr", "ne", "sa":
		return []scriptClass{scriptDevanagari}
	default:
		return []scriptClass{scriptLatin}
	}
}

func scriptName(class scriptClass) string {
	switch class {
	case scriptCyrillic:
		return "Cyrillic"
	case scriptGreek:
		return "Greek"
	case scriptHan:
		return "Han"
	case scriptKana:
		return "Kana"
	case scriptHangul:
		return "Hangul"
	case scriptArabic:
		return "Arabic"
	case scriptHebrew:
		return "Hebrew"
	case scriptDevanagari:
		return "Devanagari"
	case scriptThai:
		return "Thai"
	default:
		return "Latin"
	}
}

// classifyRune returns the writing-system family of r.
func classifyRune(r rune) scriptClass {
	switch {
	case unicode.Is(unicode.Latin, r):
		return scriptLatin
	case unicode.Is(unicode.Cyrillic, r):
		return scriptCyrillic
	case unicode.Is(unicode.Greek, r):
		return scriptGreek
	case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
		return scriptKana
	case unicode.Is(unicode.Han, r):
		return scriptHan
	case unicode.Is(unicode.Hangul, r):
		return scriptHangul
	case unicode.Is(unicode.Arabic, r):
		return scriptArabic
	case unicode.Is(unicode.Hebrew, r):
		return scriptHebrew
	case unicode.Is(unicode.Devanagari, r):
		return scriptDevanagari
	case unicode.Is(unicode.Thai, r):
		return scriptThai
	default:
		return scriptOther
	}
}

// scriptCounts counts the letters of each script family in text.
func scriptCounts(text string) map[scriptClass]int {
	counts := make(map[scriptClass]int, 8)
	for _, r := range text {
		if class := classifyRune(r); class != scriptOther {
			counts[class]++
		}
	}
	return counts
}

func totalLetters(counts map[scriptClass]int) int {
	total := 0
	for _, n := range counts {
		total += n
	}
	return total
}

// stopwordMismatch fails the text when it carries no target function word but
// clearly matches another supported Latin-script language. It abstains when the
// LexiconRegistry is not installed or the target has no configured word set.
func stopwordMismatch(text, base string) (bool, string) {
	registry := linguistics.DefaultLexiconOrNil()
	if registry == nil {
		return false, ""
	}
	// The word-count gate comes BEFORE any registry lookup: it is the cheap
	// reject for the common case (a short cue), and a profile resolve clones
	// the language's word sets.
	words := tokenizeWords(text)
	if len(words) < minWordsForStopwords {
		return false, ""
	}
	target, ok := functionWordSet(registry, base)
	if !ok {
		return false, ""
	}
	if countStopwords(words, target) > 0 {
		return false, ""
	}
	bestLang, bestHits := "", 0
	for _, lang := range lexicallyComparableLanguages {
		if lang == base {
			continue
		}
		set, ok := functionWordSet(registry, lang)
		if !ok {
			continue
		}
		if hits := countStopwords(words, set); hits > bestHits {
			bestLang, bestHits = lang, hits
		}
	}
	if bestHits >= minForeignStopwordHits {
		return true, "text carries no " + base + " function words but matches " + bestLang
	}
	return false, ""
}

// functionWordSet returns the union of a language's configured stop-words and
// function words. The union matters: the configured data is split across the
// two files (e.g. pl/ru/tr/id carry function_words.txt only), and both are
// high-frequency grammatical tokens for this purpose.
func functionWordSet(registry *linguistics.LexiconRegistry, lang string) (map[string]struct{}, bool) {
	profile, err := registry.ResolveRequired(lang)
	if err != nil {
		return nil, false
	}
	combined := make(map[string]struct{}, len(profile.StopWords)+len(profile.FunctionWords))
	for word := range profile.StopWords {
		combined[word] = struct{}{}
	}
	for word := range profile.FunctionWords {
		combined[word] = struct{}{}
	}
	if len(combined) == 0 {
		return nil, false
	}
	return combined, true
}

// tokenizeWords lowercases text and splits it into alphabetic word tokens.
func tokenizeWords(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func countStopwords(words []string, set map[string]struct{}) int {
	hits := 0
	for _, w := range words {
		if _, ok := set[w]; ok {
			hits++
		}
	}
	return hits
}
