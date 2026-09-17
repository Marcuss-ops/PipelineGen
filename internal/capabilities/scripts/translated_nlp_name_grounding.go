package scriptgeneration

import (
	"strings"
	"unicode"
	"unicode/utf8"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type localizedNameToken struct {
	start int
	end   int
	text  string
}

func findExactNameTokenSpan(text, candidate string) (scriptpkg.AnnotationSpan, bool) {
	want, have := localizedNameTokens(candidate), localizedNameTokens(text)
	if len(want) == 0 || len(have) < len(want) {
		return scriptpkg.AnnotationSpan{}, false
	}
	for start := 0; start+len(want) <= len(have); start++ {
		matched := true
		for offset := range want {
			if !strings.EqualFold(want[offset].text, have[start+offset].text) {
				matched = false
				break
			}
		}
		if matched {
			first, last := have[start], have[start+len(want)-1]
			return localizedNameSpan(text, first, last), true
		}
	}
	return scriptpkg.AnnotationSpan{}, false
}

func findInflectedPersonSpan(text, canonical, language string) (scriptpkg.AnnotationSpan, bool) {
	want, have := localizedNameTokens(canonical), localizedNameTokens(text)
	if len(want) < 2 || len(have) < len(want) {
		return scriptpkg.AnnotationSpan{}, false
	}
	for start := 0; start+len(want) <= len(have); start++ {
		matched := true
		for offset := range want {
			if !localizedInflectedNameTokenMatches(language, want[offset].text, have[start+offset].text) {
				matched = false
				break
			}
		}
		if matched {
			first, last := have[start], have[start+len(want)-1]
			return localizedNameSpan(text, first, last), true
		}
	}
	return scriptpkg.AnnotationSpan{}, false
}

func localizedNameSpan(text string, first, last localizedNameToken) scriptpkg.AnnotationSpan {
	return scriptpkg.AnnotationSpan{
		Text:      text[first.start:last.end],
		StartRune: utf8.RuneCountInString(text[:first.start]),
		EndRune:   utf8.RuneCountInString(text[:last.end]),
	}
}

func localizedInflectedNameTokenMatches(language, canonical, surface string) bool {
	canonical, surface = strings.ToLower(canonical), strings.ToLower(surface)
	if canonical == surface {
		return true
	}
	if utf8.RuneCountInString(canonical) < 3 || !strings.HasPrefix(surface, canonical) {
		return false
	}
	suffix := strings.TrimPrefix(surface, canonical)
	switch strings.ToLower(language) {
	case "pl":
		switch suffix {
		case "a", "ie", "y", "i", "u", "owi", "em", "ą", "ę", "om", "ami", "ach", "ów", "’a", "'a":
			return true
		}
	case "de":
		return suffix == "s"
	}
	return false
}

func localizedNameTokens(text string) []localizedNameToken {
	var out []localizedNameToken
	start := -1
	for offset, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || ((r == '\'' || r == '’') && start >= 0) {
			if start < 0 {
				start = offset
			}
			continue
		}
		if start >= 0 {
			out = append(out, localizedNameToken{start: start, end: offset, text: text[start:offset]})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, localizedNameToken{start: start, end: len(text), text: text[start:]})
	}
	return out
}
