package script

import (
	"regexp"
	"strings"
)

// EditorialDirective is one imperative stage direction found in authored
// narrative text. It is a detection, never a rewrite: the caller decides
// whether a run may continue.
//
// The 2026-11-25 Ilary/Totti leak read «Ricostruisci Francesco Totti a
// partire…» and «Apri con tre stacchi rapidi…» aloud because those instructions
// lived in `script_params.segments[].source_text`, the field the generator
// treats as material to narrate. Separating instructions from content is the
// structural fix; this detector is the regression that proves the separation
// held.
type EditorialDirective struct {
	// Phrase is the directive clause, from the imperative verb to the end of
	// its sentence.
	Phrase string
	// Offset is the byte offset of the directive inside the scanned text.
	Offset int
}

// editorialDirectiveRE matches only the high-precision imperative openings seen
// in real leaked payloads. Bare verbs such as "usa" or "mostra" are deliberately
// excluded: they occur as ordinary narration and would make the gate cry wolf.
var editorialDirectiveRE = regexp.MustCompile(
	`(?i)(?:^|[.!?;:]\s+|\n[ \t]*)` +
		// A directive is often introduced by a sequencing connective
		// ("Poi scrivi …", "Infine chiudi con …"). It is part of the
		// instruction, not of the directive phrase, so it is matched but
		// not captured.
		`(?:(?:poi|quindi|infine|adesso|ora|then|finally|next)\s+)?` +
		`(ricostruisci|apri con|chiudi con|non leggere|evita di|` +
		`scrivi (?:un|una|il|la|lo|le|gli|i|tre|due|quattro|cinque)|` +
		`inserisci|descrivi|narra|parti da|concludi con|` +
		`open with|start with|end with|do not read|avoid|` +
		`insert|write (?:a|an|the|one|two|three)|describe)\b`,
)

// FindEditorialDirectives returns the directive clauses in text, in reading
// order and without duplicates. An empty result means the text carries no
// recognizable stage direction.
func FindEditorialDirectives(text string) []EditorialDirective {
	matches := editorialDirectiveRE.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	directives := make([]EditorialDirective, 0, len(matches))
	for _, match := range matches {
		start, end := match[2], match[3]
		rest := text[end:]
		cut := len(rest)
		if index := strings.IndexAny(rest, ".!?\n"); index >= 0 {
			cut = index
		}
		clause := strings.TrimSpace(text[start : end+cut])
		if clause == "" {
			continue
		}
		key := strings.ToLower(clause)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		directives = append(directives, EditorialDirective{Phrase: clause, Offset: start})
	}
	return directives
}

// HasEditorialDirectives reports whether text contains an imperative editorial
// directive.
func HasEditorialDirectives(text string) bool {
	return len(FindEditorialDirectives(text)) > 0
}
