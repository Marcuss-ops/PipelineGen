// Package translation — postedit.go: pure post-editing of a translated string.
//
// WHY: the deterministic primary (Argos/OpenNMT) is fast but emits glitchy
// whitespace and spacing around punctuation ("ciao , come stai" / "ciao,come").
// The subtitle path burns that text verbatim into an ASS track, so the glitch
// is visible to the viewer. PostEdit is the canonical, deterministic cleanup
// applied to a translated string before it is persisted or burned:
//
//   - collapse every whitespace run (including newlines) to single spaces and
//     trim the ends — the ASS compiler owns line wrapping, a cue never carries
//     its own layout;
//   - never leave a space before a closing mark (, . ; : ! ? % ) ] }) or right
//     after an opening mark ( ( [ { );
//   - re-insert the single missing space when a , ; : was glued to the next
//     word by the translator ("ciao,come" → "ciao, come"), but NOT for a
//     decimal comma between digits ("1,5" stays intact).
//
// It deliberately does NOT change letter case. A subtitle cue is a sentenced
// fragment: forcing sentence case on every translated cue would capitalize a
// fragment that continues the previous cue. Sentence-case restoration belongs
// to the ASR side (the full-sentence transcript), not to the cue translator.
//
// The function is pure and idempotent: PostEdit(PostEdit(x)) == PostEdit(x).
package translation

import (
	"strings"
	"unicode"
)

// closingMarks are the characters that must never be preceded by a space.
const closingMarks = ",.;:!?%)]}…»”’"

// openingMarks are the characters that must never be followed by a space.
const openingMarks = "([{«“‘"

// PostEdit returns the canonical cleaned form of a translated string. An empty
// (or whitespace-only) input collapses to "".
func PostEdit(text string) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	if collapsed == "" {
		return ""
	}
	return normalizePunctuationSpacing(collapsed)
}

// normalizePunctuationSpacing removes the spaces the source formatting left
// around punctuation and re-inserts the single space a glued comma needs.
func normalizePunctuationSpacing(s string) string {
	in := []rune(s)
	out := make([]rune, 0, len(in))
	for i := 0; i < len(in); i++ {
		r := in[i]
		if r == ' ' {
			prev := lastRune(out)
			next := rune(0)
			if i+1 < len(in) {
				next = in[i+1]
			}
			switch {
			case next != 0 && strings.ContainsRune(closingMarks, next):
				continue // never a space before punctuation
			case prev != 0 && strings.ContainsRune(openingMarks, prev):
				continue // never a space right after an opening mark
			case prev == ' ':
				continue // collapse a run of spaces
			}
		}
		out = append(out, r)
	}
	return string(insertSpaceAfterGluedComma(out))
}

// insertSpaceAfterGluedComma inserts one space after a , ; : that was glued to
// the following word. A comma between two digits is a decimal separator and is
// left untouched.
func insertSpaceAfterGluedComma(in []rune) []rune {
	out := make([]rune, 0, len(in)+8)
	for i, r := range in {
		out = append(out, r)
		if r != ',' && r != ';' && r != ':' {
			continue
		}
		if i+1 >= len(in) {
			continue
		}
		next := in[i+1]
		if next == ' ' || !unicode.IsLetter(next) {
			continue
		}
		if i == 0 || !unicode.IsLetter(in[i-1]) {
			continue
		}
		out = append(out, ' ')
	}
	return out
}

func lastRune(runes []rune) rune {
	if len(runes) == 0 {
		return 0
	}
	return runes[len(runes)-1]
}
