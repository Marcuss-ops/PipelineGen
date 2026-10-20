package scriptgeneration

// LEXICON_MIRROR_DEBT: owner=scripts, deadline=2026-12-31 — the spoken
// number-word and date-context sets below predate numeric/month vocabulary
// in the LexiconRegistry; migrate them to config/lexicons/** once the
// registry gains that data instead of extending these literals.

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Package scriptgeneration — the numeric-value half of the entity-card
// admission path: a NUMBER annotation is only admitted when it names a
// value the source-language annotations actually carry. Extracted
// 2026-10-10 from overlay_plan.go to keep both files under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap):
// isNumericOverlayValue has exactly one caller, in this file.

// isNumericOverlayValue rejects prose that the annotation model occasionally
// mislabels as NUMBER in translated scenes. Digits cover normalized facts and
// dates; the word set preserves common spoken number forms in the supported
// output languages.
func isNumericOverlayValue(value string) bool {
	words := numericOverlayWords
	// A number mention may carry a date month, a short date connector, or a
	// unit. Other prose around digits is not a numeric card: letting one number
	// token admit an entire sentence produced mirrored date-card animations for
	// phrases such as "partendo dal 13 agosto 2007".
	context := numericOverlayContext
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
	first := strings.Trim(fields[0], "'\u2019.-.,%\u20ac$\u00a3\u00a5")
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
	_, ok := numericOverlayWords[strings.ToLower(first)]
	return ok
}

var numericOverlayWords = map[string]struct{}{
	"zero": {}, "one": {}, "two": {}, "three": {}, "four": {}, "five": {}, "six": {}, "seven": {}, "eight": {}, "nine": {}, "ten": {},
	"eleven": {}, "twelve": {}, "thirteen": {}, "fourteen": {}, "fifteen": {}, "sixteen": {}, "seventeen": {}, "eighteen": {}, "nineteen": {}, "twenty": {}, "thirty": {}, "forty": {}, "fifty": {}, "sixty": {}, "seventy": {}, "eighty": {}, "ninety": {}, "hundred": {}, "thousand": {}, "million": {}, "billion": {}, "first": {}, "second": {}, "third": {}, "fourth": {}, "fifth": {}, "sixth": {}, "seventh": {}, "eighth": {}, "ninth": {}, "tenth": {},
	"cero": {}, "uno": {}, "una": {}, "dos": {}, "tres": {}, "cuatro": {}, "cinco": {}, "seis": {}, "siete": {}, "ocho": {}, "nueve": {}, "diez": {}, "once": {}, "doce": {}, "trece": {}, "catorce": {}, "quince": {}, "dieciséis": {}, "veinte": {}, "treinta": {}, "cuarenta": {}, "cincuenta": {}, "sesenta": {}, "setenta": {}, "ochenta": {}, "noventa": {}, "cien": {}, "ciento": {}, "mil": {}, "millón": {}, "millones": {}, "primero": {}, "segunda": {}, "tercero": {}, "tercera": {},
	"due": {}, "tre": {}, "quattro": {}, "cinque": {}, "undici": {}, "dodici": {}, "tredici": {}, "quattordici": {}, "quindici": {}, "sedici": {}, "diciassette": {}, "diciotto": {}, "diciannove": {}, "venti": {}, "trenta": {}, "quaranta": {}, "cinquanta": {}, "sessanta": {}, "settanta": {}, "ottanta": {}, "novanta": {}, "cento": {}, "mille": {}, "mila": {}, "milione": {}, "milioni": {}, "primo": {}, "prima": {}, "secondo": {}, "terzo": {}, "terza": {},
	"um": {}, "uma": {}, "dois": {}, "duas": {}, "três": {}, "quatro": {}, "sete": {}, "oito": {}, "dez": {}, "onze": {}, "doze": {}, "treze": {}, "quatorze": {}, "quinze": {}, "dezesseis": {}, "vinte": {}, "trinta": {}, "quarenta": {}, "cinquenta": {}, "sessenta": {}, "oitenta": {}, "cem": {}, "milhão": {}, "milhões": {}, "primeiro": {}, "primeira": {}, "segundo": {}, "terceiro": {}, "terceira": {},
}

var numericOverlayContext = map[string]struct{}{
	"data": {}, "date": {}, "fecha": {}, "dia": {}, "día": {}, "day": {}, "anno": {}, "year": {}, "ano": {}, "año": {},
	"de": {}, "del": {}, "di": {}, "da": {}, "do": {}, "dos": {}, "el": {}, "la": {}, "l": {}, "the": {},
	"gennaio": {}, "febbraio": {}, "marzo": {}, "aprile": {}, "maggio": {}, "giugno": {}, "luglio": {}, "agosto": {}, "settembre": {}, "ottobre": {}, "novembre": {}, "dicembre": {},
	"january": {}, "february": {}, "march": {}, "april": {}, "may": {}, "june": {}, "july": {}, "august": {}, "september": {}, "october": {}, "november": {}, "december": {},
	"enero": {}, "febrero": {}, "abril": {}, "mayo": {}, "junio": {}, "julio": {}, "septiembre": {}, "octubre": {}, "diciembre": {},
	"janeiro": {}, "fevereiro": {}, "maio": {}, "junho": {}, "julho": {}, "setembro": {}, "outubro": {}, "dezembro": {},
	"anni": {}, "years": {}, "anos": {}, "months": {}, "mesi": {}, "meses": {}, "km": {}, "kg": {},
}

// cleanNumericOverlayValue reduces a dirty NLP numeric surface to the
// biggest valid numeric/date surface it contains. Trailing connectors go
// first (legacy behavior, e.g. "8 ottobre 2026 per" → "8 ottobre 2026");
// when that fails, leading prose tokens go next (e.g. the live C1 case
// "si attesta sull'18" → "18"). Leading tokens split on whitespace AND
// apostrophes so Romance elided proclitics ("sull'18" → "sull","18")
// are removable without a proclitic whitelist. Returns "" when no valid
// surface remains — the entity is dropped, never guessed.
func cleanNumericOverlayValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if isNumericOverlayValue(value) {
		return value
	}
	trimmed := value
	for !isNumericOverlayValue(trimmed) {
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			break
		}
		trimmed = strings.Join(fields[:len(fields)-1], " ")
	}
	if isNumericOverlayValue(trimmed) {
		return trimmed
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\'' || r == '\u2019'
	})
	for len(parts) > 1 {
		parts = parts[1:]
		candidate := strings.Join(parts, " ")
		if isNumericOverlayValue(candidate) && numericOverlayValueStartsWithNumber(candidate) {
			return candidate
		}
	}
	if len(parts) == 1 && isNumericOverlayValue(parts[0]) && numericOverlayValueStartsWithNumber(parts[0]) {
		return parts[0]
	}
	return ""
}

// recoverNumericSuffix re-attaches a %/currency sign when it immediately
// follows the cleaned value's grounded span in the scene text. NLP values
// often drop it ("sull'18" extracted from "sull'18%"): without the
// suffix the chart parser still reads the magnitude, but the rendered
// label would lose the unit the narration spoke.
func recoverNumericSuffix(text, value string, mentions []scriptpkg.AnnotationSpan) string {
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

func numericEntityGroundedForLanguage(scene Scene, language, sourceLanguage Language, value string) bool {
	if language == sourceLanguage {
		return true
	}
	source := annotationsForLanguage(scene, sourceLanguage, sourceLanguage)
	if source == nil {
		return false
	}
	allowed := make(map[int64]struct{})
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), source.PrimaryEntities...), source.SecondaryEntities...) {
		if capabilityoverlay.EntityTypeToKind(entity.Type) != capabilityoverlay.KindNumber {
			continue
		}
		for _, number := range numericValues(entity.CanonicalName) {
			allowed[number] = struct{}{}
		}
	}
	values := numericValues(value)
	if len(values) == 0 {
		return false
	}
	for _, number := range values {
		if _, ok := allowed[number]; !ok {
			return false
		}
	}
	return true
}

func numericValues(value string) []int64 {
	words := map[string]int64{
		"zero": 0, "cero": 0, "uno": 1, "una": 1, "one": 1, "um": 1, "uma": 1, "un": 1,
		"two": 2, "dos": 2, "due": 2, "dois": 2, "duas": 2,
		"three": 3, "tres": 3, "tre": 3, "três": 3,
		"four": 4, "cuatro": 4, "quattro": 4, "quatro": 4,
		"five": 5, "cinco": 5, "cinque": 5,
		"six": 6, "seis": 6, "sei": 6,
		"seven": 7, "siete": 7, "sette": 7,
		"eight": 8, "ocho": 8, "otto": 8, "oito": 8,
		"nine": 9, "nueve": 9, "nove": 9,
		"ten": 10, "diez": 10, "dieci": 10, "dez": 10,
		"eleven": 11, "once": 11, "undici": 11, "onze": 11,
		"twelve": 12, "doce": 12, "dodici": 12, "doze": 12,
		"thirteen": 13, "trece": 13, "tredici": 13, "treze": 13,
		"fourteen": 14, "catorce": 14, "quattordici": 14, "quatorze": 14,
		"fifteen": 15, "quince": 15, "quindici": 15, "quinze": 15,
		"sixteen": 16, "dieciséis": 16, "sedici": 16, "dezesseis": 16,
		"seventeen": 17, "diciassette": 17,
		"eighteen": 18, "diciotto": 18,
		"nineteen": 19, "diciannove": 19,
		"twenty": 20, "veinte": 20, "venti": 20, "vinte": 20,
		"thirty": 30, "treinta": 30, "trenta": 30, "trinta": 30,
		"forty": 40, "cuarenta": 40, "quaranta": 40,
		"fifty": 50, "cincuenta": 50, "cinquanta": 50,
		"sixty": 60, "sesenta": 60, "sessanta": 60,
		"seventy": 70, "setenta": 70, "settanta": 70,
		"eighty": 80, "ochenta": 80, "ottanta": 80,
		"ninety": 90, "noventa": 90, "novanta": 90,
		"hundred": 100, "cien": 100, "ciento": 100, "cento": 100, "cem": 100,
	}
	tokens := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var values []int64
	current := int64(0)
	flush := func() {
		if current != 0 {
			values = append(values, current)
			current = 0
		}
	}
	for _, token := range tokens {
		if number, err := strconv.ParseInt(token, 10, 64); err == nil {
			flush()
			values = append(values, number)
			continue
		}
		if token == "and" || token == "e" || token == "y" || token == "de" || token == "del" {
			continue
		}
		number, ok := words[token]
		if !ok {
			flush()
			continue
		}
		if number >= 20 && number%10 == 0 {
			current += number
		} else if number == 100 {
			if current == 0 {
				current = 100
			} else {
				current *= 100
			}
		} else {
			current += number
		}
	}
	flush()
	return values
}
