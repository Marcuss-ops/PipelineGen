package scriptgeneration

import (
	"regexp"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Ground explicit unit-bearing quantities in the final scene text even when
// VidRush omitted them from NER. Bare dates and years remain in the timeline
// date lane.
var groundedSceneNumberRE = regexp.MustCompile(`(?i)\b\d+(?:[.,]\d+)?\s+(?:ann[io]|years?|mes[ie]|months?|giorn[io]|days?|or[ae]|hours?|minut[io]|minutes?|second[io]|seconds?|euro|euros|chilometri|kilometri|km|metri|meters?|metres?|kg|chilogrammi|percento|per\s+cento)\b`)

func appendGroundedNumberEntities(ann *scriptpkg.SceneAnnotations, text string) {
	if ann == nil {
		return
	}
	for _, match := range groundedSceneNumberRE.FindAllString(text, -1) {
		mentions := findAllEntitySpans(text, match)
		if len(mentions) == 0 || overlapsGroundedNumber(ann, mentions) {
			continue
		}
		value := strings.Join(strings.Fields(match), " ")
		ann.SecondaryEntities = append(ann.SecondaryEntities, scriptpkg.AnnotatedEntity{
			ID: "entity-" + safeEntityID(value), Text: value,
			CanonicalName: value, CanonicalEntityID: "number:" + safeEntityID(value),
			Type: "NUMBER", Confidence: 1, Mentions: mentions,
		})
	}
}

func overlapsGroundedNumber(ann *scriptpkg.SceneAnnotations, mentions []scriptpkg.AnnotationSpan) bool {
	entities := append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...)
	for _, entity := range entities {
		switch normalizeEntityAnnotationType(entity.Type) {
		case "NUMBER", "CARDINAL", "MONEY", "PERCENT", "QUANTITY":
		default:
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
