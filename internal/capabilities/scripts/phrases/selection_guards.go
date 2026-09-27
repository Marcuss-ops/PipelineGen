// Package phrases — selection_guards.go holds the candidate guards the
// selector applies before a phrase surface is accepted: the proper-name /
// acronym rule, the configured visual-verb check and the blocked-rune-span
// overlap test. Split from selection.go to keep that file under the
// max_lines_per_file_strict=600 cap.
package phrases

import (
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

func isConfiguredVisualVerb(word string, profile *linguistics.LexiconProfile) bool {
	if profile == nil {
		return false
	}
	_, ok := profile.VisualVerbs[word]
	return ok
}

// ContainsProperNamePair reports whether value carries two consecutive
// capitalised words. The selector is the single owner of this rule: it keeps a
// phrase surface free of proper-name runs even when the entity extractor missed
// a name, validating candidates without rewriting them.
//
// A word that is ENTIRELY uppercase with two or more letters is an acronym
// (USA, NATO, AI), not a capitalised name word: it neither starts nor continues
// a proper-name run. Without this, adjacent acronyms ("USA NATO") or a shouted
// sentence would be rejected as if they carried a person's name.
func ContainsProperNamePair(value string) bool {
	previousTitle := false
	// FieldsSeq iterates without materialising the []string that
	// strings.Fields would allocate for every candidate phrase.
	for raw := range strings.FieldsSeq(value) {
		word := strings.Trim(raw, ".,;:!?\"'’()[]{}")
		currentTitle := false
		if !isAcronymToken(word) {
			for _, r := range word {
				currentTitle = unicode.IsUpper(r)
				break
			}
		}
		if currentTitle && previousTitle {
			return true
		}
		previousTitle = currentTitle
	}
	return false
}

// isAcronymToken reports whether word is an all-uppercase token of two or more
// letters (USA, NATO, AI). A single capital ("I", "A") is an ordinary
// capitalised word, not an acronym, so it keeps its previous meaning.
func isAcronymToken(word string) bool {
	letters := 0
	for _, r := range word {
		if !unicode.IsLetter(r) {
			continue
		}
		if !unicode.IsUpper(r) {
			return false
		}
		letters++
	}
	return letters >= 2
}

func overlapsAnyRuneSpan(start, end int, spans [][2]int) bool {
	for _, span := range spans {
		if start < span[1] && span[0] < end {
			return true
		}
	}
	return false
}
