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
//  1. Script check (confident). For a target written in a non-Latin script
//     (Russian → Cyrillic, Japanese → Han/Kana, Korean → Hangul, Arabic,
//     Hebrew, Greek, Thai, Devanagari) a translation with ZERO letters of the
//     expected script is not that language — full stop.
//  2. Function-word check (heuristic). For the Latin-script targets the script
//     cannot distinguish languages, so VerifyTargetLanguage scores the text
//     against small function-word sets. It only fails when the text carries NO
//     function word of the target AND clearly matches another supported
//     language. A technical/vocabulary fragment with no function words at all
//     therefore stays accepted (no false positive on "rendering pipeline 4K").
//
// godlike/07: the verifier is a pure function over the text; it never guesses a
// language from a truncated sample (a text with fewer than minWordsForStopwords
// words is accepted without a stopword verdict).
package translation

import (
	"strings"
	"unicode"
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
// check passes for them and the stopword check does the real work.
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
// clearly matches another supported Latin-script language.
func stopwordMismatch(text, base string) (bool, string) {
	target, known := stopwords[base]
	if !known {
		return false, "" // no table for this target: abstain
	}
	words := tokenizeWords(text)
	if len(words) < minWordsForStopwords {
		return false, ""
	}
	if countStopwords(words, target) > 0 {
		return false, ""
	}
	bestLang, bestHits := "", 0
	for _, lang := range latinStopwordLanguages {
		if lang == base {
			continue
		}
		if hits := countStopwords(words, stopwords[lang]); hits > bestHits {
			bestLang, bestHits = lang, hits
		}
	}
	if bestHits >= minForeignStopwordHits {
		return true, "text carries no " + base + " function words but matches " + bestLang
	}
	return false, ""
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

// latinStopwordLanguages is the fixed iteration order of the function-word
// comparison, so the verdict is deterministic even when two languages tie.
var latinStopwordLanguages = []string{"en", "it", "es", "de", "fr", "pt", "nl", "pl", "tr", "id"}

func stopwordSet(words ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		set[w] = struct{}{}
	}
	return set
}

// stopwords holds a small, high-signal function-word set per Latin-script
// target. Precision matters more than recall: a false negative (abstaining)
// costs nothing, a false positive would reject a good translation.
var stopwords = map[string]map[string]struct{}{
	"en": stopwordSet("the", "a", "an", "and", "or", "but", "of", "to", "in", "on", "for", "with", "is", "are", "was", "were", "that", "this", "it", "as", "by", "at", "from", "not"),
	"it": stopwordSet("il", "lo", "la", "i", "gli", "le", "un", "uno", "una", "e", "ed", "o", "ma", "di", "a", "da", "in", "con", "per", "su", "che", "non", "è", "sono", "del", "della", "dei", "più"),
	"es": stopwordSet("el", "la", "los", "las", "un", "una", "y", "e", "o", "u", "pero", "de", "del", "en", "con", "por", "para", "que", "no", "es", "son", "al", "se", "su", "más"),
	"de": stopwordSet("der", "die", "das", "den", "dem", "des", "ein", "eine", "einen", "und", "oder", "aber", "von", "zu", "im", "in", "mit", "für", "auf", "ist", "sind", "nicht", "dass", "es", "auch"),
	"fr": stopwordSet("le", "la", "les", "un", "une", "des", "et", "ou", "mais", "de", "du", "en", "dans", "avec", "pour", "sur", "que", "qui", "ne", "pas", "est", "sont", "ce", "cette", "plus"),
	"pt": stopwordSet("o", "a", "os", "as", "um", "uma", "e", "ou", "mas", "de", "do", "da", "em", "com", "por", "para", "que", "não", "é", "são", "no", "na", "se", "mais"),
	"nl": stopwordSet("de", "het", "een", "en", "of", "maar", "van", "in", "met", "voor", "op", "is", "zijn", "niet", "dat", "dit", "die", "te", "aan", "ook"),
	"pl": stopwordSet("i", "oraz", "lub", "ale", "nie", "jest", "są", "to", "że", "się", "na", "w", "z", "do", "dla", "po", "od", "jak", "tego", "tym", "więc"),
	"tr": stopwordSet("ve", "veya", "ama", "fakat", "bir", "bu", "şu", "o", "ile", "için", "de", "da", "değil", "çok", "daha", "olarak", "ise", "ne", "ki", "gibi"),
	"id": stopwordSet("dan", "atau", "tapi", "tetapi", "tidak", "yang", "di", "ke", "dari", "untuk", "dengan", "ini", "itu", "adalah", "pada", "sebagai", "karena", "juga", "akan", "oleh"),
}
