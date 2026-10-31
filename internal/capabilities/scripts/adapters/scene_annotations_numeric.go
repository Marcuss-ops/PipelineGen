package adapters

import (
	"regexp"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// The semantic model may omit a spoken quantity even when the script contains
// an explicit numeral. Preserve exact, unit-bearing spans as NUMBER
// annotations so the numeric overlay lane can use certified speech timing.
// Date parts and bare years are deliberately not matched by this rule.
var groundedNumberUnitRE = regexp.MustCompile(`(?i)\b\d+(?:[.,]\d+)?\s+(?:ann[io]|years?|mes[ie]|months?|giorn[io]|days?|or[ae]|hours?|minut[io]|minutes?|second[io]|seconds?|euro|euros|chilometri|kilometri|km|metri|meters?|metres?|kg|chilogrammi|percento|per\s+cento)\b`)

func appendGroundedNumberEntities(ann *scriptpkg.SceneAnnotations, text string) {
	if ann == nil {
		return
	}
	for _, value := range groundedNumberUnitRE.FindAllString(text, -1) {
		value = strings.Join(strings.Fields(value), " ")
		mentions := findAllAnnotationSpans(text, value)
		if len(mentions) == 0 || overlapsExistingNumberEntity(ann, mentions) {
			continue
		}
		canonicalID := "number:" + safeAnnotationID(value)
		ann.SecondaryEntities = append(ann.SecondaryEntities, scriptpkg.AnnotatedEntity{
			ID: "entity-" + safeAnnotationID(value), Text: value,
			CanonicalName: value, CanonicalEntityID: canonicalID,
			Type: "NUMBER", Confidence: 1, Mentions: mentions,
		})
	}
}

func overlapsExistingNumberEntity(ann *scriptpkg.SceneAnnotations, mentions []scriptpkg.AnnotationSpan) bool {
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
		kind := normalizeAnnotationType(entity.Type)
		if kind != "NUMBER" && kind != "CARDINAL" && kind != "MONEY" && kind != "PERCENT" && kind != "QUANTITY" {
			continue
		}
		for _, existing := range entity.Mentions {
			for _, candidate := range mentions {
				if existing.StartRune < candidate.EndRune && candidate.StartRune < existing.EndRune {
					return true
				}
			}
		}
	}
	return false
}
