// Package scriptgeneration — overlay_numeric_grounding.go: the numeric-value half
// of the entity-card admission path. A NUMBER annotation is admitted only when it
// names a value the source-language annotations actually carry.
// isNumericOverlayValue has exactly one caller, in this file: the numeric rescue
// path may recover a value the annotation layer lost, and may never invent one to
// fill a card.
package overlayproject

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// isNumericOverlayValue rejects prose that the annotation model occasionally
// mislabels as NUMBER in translated scenes. Digits cover normalized facts and
// dates; the word set preserves common spoken number forms in the supported
// output languages.
func IsNumericOverlayValue(value string) bool {
	words := numericOverlayWordSet()
	// A number mention may carry a date month, a short date connector, or a
	// unit. Other prose around digits is not a numeric card: letting one number
	// token admit an entire sentence produced mirrored date-card animations for
	// phrases such as "partendo dal 13 agosto 2007".
	context := numericOverlayContextSet()
	var token strings.Builder
	sawNumber := false
	valid := true
	flush := func() bool {
		if token.Len() == 0 {
			return false
		}
		word := strings.ToLower(token.String())
		token.Reset()
		if _, ok := words[word]; ok {
			sawNumber = true
			return true
		}
		if _, ok := context[word]; !ok {
			valid = false
		}
		return false
	}
	for _, r := range value {
		if unicode.IsDigit(r) {
			sawNumber = true
			continue
		}
		if unicode.IsLetter(r) {
			token.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return valid && sawNumber
}

// numericOverlayValueStartsWithNumber reports whether the first token of a
// candidate numeric surface is itself a number (digits or a number word).
// Cleaned candidates must pass it: without this guard a leading date-context
// token satisfies isNumericOverlayValue on its own, so "dove si registra
// solamente l'18" cleaned to "l 18" (the "l" is a legitimate date connector)
// and then failed to ground, dropping a spoken value (live M1 regression).
func numericOverlayValueStartsWithNumber(value string) bool {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return false
	}
	first := strings.Trim(fields[0], "'\u2019.-,%\u20ac$\u00a3\u00a5")
	if first == "" {
		return false
	}
	for _, r := range first {
		if unicode.IsDigit(r) {
			return true
		}
		if !unicode.IsLetter(r) {
			return false
		}
	}
	_, ok := numericOverlayWordSet()[strings.ToLower(first)]
	return ok
}

// numericOverlayWordSet is the cross-language spoken-number vocabulary used by
// the entity-card admission path. It lives in
// the fallback profile's numeric_overlay_words set (the fallback profile is
// the cross-linguistic set, because a numeric surface may arrive in any output
// language), so the check stays language-agnostic without a second copy here.
func numericOverlayWordSet() map[string]struct{} {
	return linguistics.DefaultProfiles().WordSet("fallback", "numeric_overlay_words")
}

// numericOverlayContextSet is the date-context vocabulary (day/year words,
// connectors, month names and units) from
// The same applies to numeric_overlay_context.
func numericOverlayContextSet() map[string]struct{} {
	return linguistics.DefaultProfiles().WordSet("fallback", "numeric_overlay_context")
}

// CleanNumericOverlayValue reduces a dirty NLP numeric surface to the
// biggest valid numeric/date surface it contains. Trailing connectors go
// first (legacy behavior, e.g. "8 ottobre 2026 per" → "8 ottobre 2026");
// when that fails, leading prose tokens go next (e.g. the live C1 case
// "si attesta sull'18" → "18"). Leading tokens split on whitespace AND
// apostrophes so Romance elided proclitics ("sull'18" → "sull","18")
// are removable without a proclitic whitelist. Returns "" when no valid
// surface remains — the entity is dropped, never guessed.
func CleanNumericOverlayValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if IsNumericOverlayValue(value) {
		return value
	}
	trimmed := value
	for !IsNumericOverlayValue(trimmed) {
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			break
		}
		trimmed = strings.Join(fields[:len(fields)-1], " ")
	}
	if IsNumericOverlayValue(trimmed) {
		return trimmed
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\'' || r == '\u2019'
	})
	for len(parts) > 1 {
		parts = parts[1:]
		candidate := strings.Join(parts, " ")
		if IsNumericOverlayValue(candidate) && numericOverlayValueStartsWithNumber(candidate) {
			return candidate
		}
	}
	if len(parts) == 1 && IsNumericOverlayValue(parts[0]) && numericOverlayValueStartsWithNumber(parts[0]) {
		return parts[0]
	}
	return ""
}

// RecoverNumericSuffix re-attaches a %/currency sign when it immediately
// follows the cleaned value's grounded span in the scene text. NLP values
// often drop it ("sull'18" extracted from "sull'18%"): without the
// suffix the chart parser still reads the magnitude, but the rendered
// label would lose the unit the narration spoke.
func RecoverNumericSuffix(text, value string, mentions []scriptpkg.AnnotationSpan) string {
	if value == "" {
		return value
	}
	if last, _ := utf8.DecodeLastRuneInString(value); !unicode.IsDigit(last) {
		return value
	}
	runes := []rune(text)
	for _, mention := range mentions {
		if mention.StartRune < 0 || mention.EndRune > len(runes) || mention.StartRune >= mention.EndRune {
			continue
		}
		segment := string(runes[mention.StartRune:mention.EndRune])
		at := strings.Index(segment, value)
		if at < 0 {
			continue
		}
		absoluteEnd := mention.StartRune + len([]rune(segment[:at])) + len([]rune(value))
		if absoluteEnd >= len(runes) {
			return value
		}
		switch runes[absoluteEnd] {
		case '%', '€', '$', '£', '¥':
			return value + string(runes[absoluteEnd])
		}
		return value
	}
	return value
}
