package scriptgeneration

import (
	"strconv"
	"strings"
	"unicode"

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
	words := map[string]struct{}{
		"zero": {}, "one": {}, "two": {}, "three": {}, "four": {}, "five": {}, "six": {}, "seven": {}, "eight": {}, "nine": {}, "ten": {},
		"eleven": {}, "twelve": {}, "thirteen": {}, "fourteen": {}, "fifteen": {}, "sixteen": {}, "seventeen": {}, "eighteen": {}, "nineteen": {}, "twenty": {}, "thirty": {}, "forty": {}, "fifty": {}, "sixty": {}, "seventy": {}, "eighty": {}, "ninety": {}, "hundred": {}, "thousand": {}, "million": {}, "billion": {}, "first": {}, "second": {}, "third": {}, "fourth": {}, "fifth": {}, "sixth": {}, "seventh": {}, "eighth": {}, "ninth": {}, "tenth": {},
		"cero": {}, "uno": {}, "una": {}, "dos": {}, "tres": {}, "cuatro": {}, "cinco": {}, "seis": {}, "siete": {}, "ocho": {}, "nueve": {}, "diez": {}, "once": {}, "doce": {}, "trece": {}, "catorce": {}, "quince": {}, "dieciséis": {}, "veinte": {}, "treinta": {}, "cuarenta": {}, "cincuenta": {}, "sesenta": {}, "setenta": {}, "ochenta": {}, "noventa": {}, "cien": {}, "ciento": {}, "mil": {}, "millón": {}, "millones": {}, "primero": {}, "segunda": {}, "tercero": {}, "tercera": {},
		"due": {}, "tre": {}, "quattro": {}, "cinque": {}, "undici": {}, "dodici": {}, "tredici": {}, "quattordici": {}, "quindici": {}, "sedici": {}, "diciassette": {}, "diciotto": {}, "diciannove": {}, "venti": {}, "trenta": {}, "quaranta": {}, "cinquanta": {}, "sessanta": {}, "settanta": {}, "ottanta": {}, "novanta": {}, "cento": {}, "mille": {}, "mila": {}, "milione": {}, "milioni": {}, "primo": {}, "prima": {}, "secondo": {}, "terzo": {}, "terza": {},
		"um": {}, "uma": {}, "dois": {}, "duas": {}, "três": {}, "quatro": {}, "sete": {}, "oito": {}, "dez": {}, "onze": {}, "doze": {}, "treze": {}, "quatorze": {}, "quinze": {}, "dezesseis": {}, "vinte": {}, "trinta": {}, "quarenta": {}, "cinquenta": {}, "sessenta": {}, "oitenta": {}, "cem": {}, "milhão": {}, "milhões": {}, "primeiro": {}, "primeira": {}, "segundo": {}, "terceiro": {}, "terceira": {},
	}
	// A number mention may carry a date month, a short date connector, or a
	// unit. Other prose around digits is not a numeric card: letting one number
	// token admit an entire sentence produced mirrored date-card animations for
	// phrases such as "partendo dal 13 agosto 2007".
	context := map[string]struct{}{
		"data": {}, "date": {}, "fecha": {}, "dia": {}, "día": {}, "day": {}, "anno": {}, "year": {}, "ano": {}, "año": {},
		"de": {}, "del": {}, "di": {}, "da": {}, "do": {}, "dos": {}, "el": {}, "la": {}, "l": {}, "the": {},
		"gennaio": {}, "febbraio": {}, "marzo": {}, "aprile": {}, "maggio": {}, "giugno": {}, "luglio": {}, "agosto": {}, "settembre": {}, "ottobre": {}, "novembre": {}, "dicembre": {},
		"january": {}, "february": {}, "march": {}, "april": {}, "may": {}, "june": {}, "july": {}, "august": {}, "september": {}, "october": {}, "november": {}, "december": {},
		"enero": {}, "febrero": {}, "abril": {}, "mayo": {}, "junio": {}, "julio": {}, "septiembre": {}, "octubre": {}, "diciembre": {},
		"janeiro": {}, "fevereiro": {}, "maio": {}, "junho": {}, "julho": {}, "setembro": {}, "outubro": {}, "dezembro": {},
		"anni": {}, "years": {}, "anos": {}, "months": {}, "mesi": {}, "meses": {}, "km": {}, "kg": {},
	}
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
