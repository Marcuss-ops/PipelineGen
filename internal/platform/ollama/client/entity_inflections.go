package client

import (
	"strings"
	"unicode"
)

type entityTokenSpan struct {
	start int
	end   int
	text  string
}

func russianInflectedEntitySurface(segment, candidate string) string {
	return inflectedEntitySurface(segment, candidate, russianInflectedTokenMatches)
}

func polishInflectedEntitySurface(segment, candidate string) string {
	return inflectedEntitySurface(segment, candidate, polishInflectedTokenMatches)
}

func inflectedEntitySurface(segment, candidate string, tokenMatches func(string, string) bool) string {
	candidateTokens, sourceTokens := entityTokenSpans(candidate), entityTokenSpans(segment)
	if len(candidateTokens) == 0 || len(sourceTokens) < len(candidateTokens) {
		return ""
	}
	for start := 0; start+len(candidateTokens) <= len(sourceTokens); start++ {
		matched := true
		for offset, token := range candidateTokens {
			if !tokenMatches(token.text, sourceTokens[start+offset].text) {
				matched = false
				break
			}
		}
		if matched {
			return segment[sourceTokens[start].start:sourceTokens[start+len(candidateTokens)-1].end]
		}
	}
	return ""
}

func russianInflectedTokenMatches(candidate, surface string) bool {
	candidate, surface = strings.ToLower(candidate), strings.ToLower(surface)
	if candidate == surface {
		return true
	}
	if len([]rune(candidate)) < 3 || !strings.HasPrefix(surface, candidate) {
		return false
	}
	switch strings.TrimPrefix(surface, candidate) {
	case "а", "я", "ы", "и", "е", "у", "ю", "ом", "ем", "ой", "ей", "ов", "ев", "ам", "ям", "ах", "ях", "ами", "ями", "ого", "его", "ому", "ему", "ою", "ею":
		return true
	default:
		return false
	}
}

func polishInflectedTokenMatches(candidate, surface string) bool {
	candidate, surface = strings.ToLower(candidate), strings.ToLower(surface)
	if candidate == surface {
		return true
	}
	if len([]rune(candidate)) < 3 || !strings.HasPrefix(surface, candidate) {
		return false
	}
	switch strings.TrimPrefix(surface, candidate) {
	case "a", "ie", "y", "i", "u", "owi", "em", "ą", "ę", "om", "ami", "ach", "ów", "’a", "'a":
		return true
	default:
		return false
	}
}

func entityTokenSpans(text string) []entityTokenSpan {
	var spans []entityTokenSpan
	start := -1
	for offset, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || ((r == '\'' || r == '’') && start >= 0) {
			if start < 0 {
				start = offset
			}
			continue
		}
		if start >= 0 {
			spans = append(spans, entityTokenSpan{start: start, end: offset, text: text[start:offset]})
			start = -1
		}
	}
	if start >= 0 {
		spans = append(spans, entityTokenSpan{start: start, end: len(text), text: text[start:]})
	}
	return spans
}
