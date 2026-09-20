// Package youtube — whisper_cues.go: canonical cleanup of Whisper output.
//
// WHY: Whisper emits more than speech. Real transcripts carry non-speech tags
// ("[Music]", "(applause)", "[BLANK_AUDIO]"), music symbols (♪), speaker-change
// markers (">>"), orphan punctuation, repeated hallucination segments and
// un-cased text. Every one of those reaches the subtitle track verbatim and is
// then TRANSLATED into ten languages — a clip that opens on "[Music]" opens on
// ten localized "[Music]" captions.
//
// This file is the single owner of the ASR-side cleanup:
//
//   - strip non-speech tags, music symbols and speaker markers;
//   - collapse whitespace, drop orphan punctuation;
//   - drop cues that carry no speech at all, and consecutive hallucination
//     duplicates;
//   - restore sentence case on the full transcript (never on an individual
//     cue: a cue is a fragment and must not be re-capitalized mid-sentence).
//
// The functions are pure and deterministic: same cues, same output.
package youtube

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// asrNoiseTag matches a non-speech bracket tag. The inner vocabulary is the
// canonical Whisper hallucination set (multilingual: the model emits the tag in
// the audio's language).
var asrNoiseTag = regexp.MustCompile(`(?i)[\[\(]\s*(music|musica|musique|música|musik|applause|applausi|applaudissements|laughter|laughs|risas|cheering|cheers|blank_audio|silence|silencio|inaudible|indistinct|sound effect|noise|background noise)\s*[\]\)]`)

// asrSpeakerMarker matches the leading ">>" speaker-change marker.
var asrSpeakerMarker = regexp.MustCompile(`^\s*>>+\s*`)

// asrMusicSymbols removes the music glyphs Whisper writes around humming/song.
var asrMusicSymbols = strings.NewReplacer("♪", "", "♫", "", "♬", "")

// asrCueDuplicateWindowMs is how close a repeated identical cue must start to
// the previous cue to be treated as a hallucination loop.
const asrCueDuplicateWindowMs = 250

// whisperLowLanguageConfidence is the language-probability floor below which
// the adapter logs the detected source language as unreliable. The transcript
// is still returned: the warning is a signal for operators, not a gate that
// discards real speech over a language guess.
const whisperLowLanguageConfidence = 0.35

// CleanASRText strips the non-speech artifacts above from one transcript
// fragment and returns the remaining prose (possibly empty).
func CleanASRText(text string) string {
	cleaned := asrSpeakerMarker.ReplaceAllString(text, "")
	cleaned = asrNoiseTag.ReplaceAllString(cleaned, " ")
	cleaned = asrMusicSymbols.Replace(cleaned)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	cleaned = strings.TrimLeft(cleaned, " \t-–—.,;:!?\"'()[]{}")
	cleaned = strings.TrimRight(cleaned, " \t-–—")
	return strings.TrimSpace(cleaned)
}

// CleanWhisperCues applies CleanASRText to every cue, drops cues with no usable
// window or no speech left, and drops an immediate hallucination duplicate of
// the previous cue. An input with no surviving cue yields nil.
func CleanWhisperCues(cues []detail.TimedCue) []detail.TimedCue {
	out := make([]detail.TimedCue, 0, len(cues))
	for _, cue := range cues {
		if cue.EndMs <= cue.StartMs {
			continue
		}
		text := CleanASRText(cue.Text)
		if text == "" {
			continue
		}
		if n := len(out); n > 0 && isCueDuplicate(out[n-1], cue.StartMs, text) {
			continue
		}
		out = append(out, detail.TimedCue{StartMs: cue.StartMs, EndMs: cue.EndMs, Text: text})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// JoinCueTexts rebuilds a transcript string from cue text, in order.
func JoinCueTexts(cues []detail.TimedCue) string {
	parts := make([]string, 0, len(cues))
	for _, cue := range cues {
		if text := strings.TrimSpace(cue.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

// isCueDuplicate reports whether text repeats the previous cue within the
// hallucination window.
func isCueDuplicate(previous detail.TimedCue, startMs int64, text string) bool {
	if startMs > previous.EndMs+asrCueDuplicateWindowMs {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(previous.Text), text)
}

// usesLetterCase reports whether a base language's script distinguishes upper
// and lower case. Scripts without case (Han, Kana, Hangul, Arabic, Hebrew,
// Thai, Devanagari) are left untouched.
func usesLetterCase(base string) bool {
	switch base {
	case "ja", "zh", "yue", "ko", "ar", "fa", "ur", "he", "yi", "th", "hi", "mr", "ne", "sa":
		return false
	default:
		return true
	}
}

// baseLanguageCode returns the lowercase primary subtag of a BCP-47 tag.
func baseLanguageCode(tag string) string {
	tag = strings.TrimSpace(strings.ToLower(tag))
	if i := strings.IndexAny(tag, "-_"); i >= 0 {
		return tag[:i]
	}
	return tag
}

// RestoreSentenceCase uppercases the first letter of every sentence for scripts
// that have letter case. It is a no-op for un-cased scripts and for a text that
// is already cased. It is applied to a FULL transcript, never to a single cue.
func RestoreSentenceCase(text, lang string) string {
	if text == "" || !usesLetterCase(baseLanguageCode(lang)) {
		return text
	}
	runes := []rune(text)
	capitalizeNext := true
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case capitalizeNext && unicode.IsLetter(r):
			runes[i] = unicode.ToUpper(r)
			capitalizeNext = false
		case r == '!' || r == '?':
			capitalizeNext = true
		case r == '.':
			if asrSentenceEndingDot(runes, i) {
				capitalizeNext = true
			}
		case unicode.IsLetter(r):
			capitalizeNext = false
		}
	}
	return string(runes)
}

// asrSentenceEndingDot reports whether the dot at index i ends a sentence. A dot
// inside a token ("e.g", "U.S") is an abbreviation, not a sentence end.
func asrSentenceEndingDot(runes []rune, i int) bool {
	start := i
	for start > 0 && runes[start-1] != ' ' {
		start--
	}
	token := runes[start:i]
	for _, r := range token {
		if r == '.' {
			return false
		}
	}
	if len(token) < 2 {
		return false
	}
	j := i + 1
	for j < len(runes) && runes[j] == ' ' {
		j++
	}
	if j >= len(runes) {
		return true
	}
	return unicode.IsLetter(runes[j])
}
