package scriptgeneration

import (
	"strings"
	"unicode"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// completeGroundedPhrase retains a phrase only when it begins at a sentence
// boundary and ends at sentence punctuation. It extends a grounded span over
// terminal punctuation when the selector returned the sentence without it.
func completeGroundedPhrase(text string, span scriptpkg.AnnotationSpan) (scriptpkg.AnnotationSpan, bool) {
	runes := []rune(text)
	start, end := span.StartRune, span.EndRune
	if start < 0 || end <= start || end > len(runes) {
		return scriptpkg.AnnotationSpan{}, false
	}
	for i := start - 1; i >= 0; i-- {
		if unicode.IsSpace(runes[i]) {
			continue
		}
		if !sentenceBoundaryRune(runes[i]) {
			return scriptpkg.AnnotationSpan{}, false
		}
		break
	}
	terminal := false
	for i := end - 1; i >= start; i-- {
		if unicode.IsSpace(runes[i]) {
			continue
		}
		terminal = sentenceBoundaryRune(runes[i])
		break
	}
	if !terminal {
		for i := end; i < len(runes); i++ {
			if unicode.IsSpace(runes[i]) {
				continue
			}
			if !sentenceBoundaryRune(runes[i]) {
				return scriptpkg.AnnotationSpan{}, false
			}
			end = i + 1
			terminal = true
			break
		}
	}
	if !terminal {
		return scriptpkg.AnnotationSpan{}, false
	}
	span.Text = strings.TrimSpace(string(runes[start:end]))
	span.EndRune = end
	return span, true
}

func sentenceBoundaryRune(r rune) bool {
	switch r {
	case '.', '?', '!', ';', ':':
		return true
	default:
		return false
	}
}
