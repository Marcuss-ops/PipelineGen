package scriptgeneration

import (
	"regexp"
	"strings"
	"unicode/utf8"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Ground explicit unit-bearing quantities in the final scene text even when
// VidRush omitted them from NER. Bare dates and years remain in the timeline
// date lane.
var groundedSceneNumberRE = regexp.MustCompile(`(?i)\b\d+(?:[.,]\d+)?\s+(?:ann[io]|years?|mes[ie]|months?|giorn[io]|days?|or[ae]|hours?|minut[io]|minutes?|second[io]|seconds?|euro|euros|chilometri|kilometri|km|metri|meters?|metres?|kg|chilogrammi|percento|per\s+cento)\b`)

func isUppercaseAcronym(value string) bool {
	letters := 0
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return false
		}
		letters++
	}
	return letters >= 2 && letters <= 6
}

func startsWithEntityAcronym(value string) bool {
	fields := strings.Fields(strings.TrimSpace(value))
	return len(fields) > 1 && isUppercaseAcronym(fields[0])
}

func normalizeEntityImageName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(strings.TrimSuffix(value, "'s"), "’s")
	return strings.Join(strings.Fields(value), " ")
}

// advanceRunes returns the byte offset n runes after start, or -1 when the
// string ends first. It lets a caller compare a fixed-rune-length window
// without allocating a rune slice for the text.
func advanceRunes(text string, start, n int) int {
	offset := start
	for i := 0; i < n; i++ {
		if offset >= len(text) {
			return -1
		}
		_, size := utf8.DecodeRuneInString(text[offset:])
		if size <= 0 {
			return -1
		}
		offset += size
	}
	return offset
}

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
