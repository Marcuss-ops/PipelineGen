package scriptgeneration

import phrasepkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/phrases"

// deterministicImportantPhrases keeps the scripts package's local call shape
// while the pure selector lives in its leaf package. Entity spans are resolved
// here because only this package owns VisualEntity and its grounding rules.
func deterministicImportantPhrases(text string, entities []VisualEntity, limit int, language string) []string {
	blocked := make([][2]int, 0, len(entities))
	for _, entity := range entities {
		span, ok := findEntitySpan(text, entity.Text)
		if ok {
			blocked = append(blocked, [2]int{span.StartRune, span.EndRune})
		}
	}
	return phrasepkg.ImportantPhrases(text, blocked, limit, language)
}

func deterministicImportantWords(phrases []string, limit int, language string) []string {
	return phrasepkg.ImportantWords(phrases, limit, language)
}
