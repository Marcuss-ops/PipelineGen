package adapters

import (
	"strings"
	"unicode"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// RebaseSceneAnnotations is the final output-boundary normalization. It is
// exported for the use-case finalizer so persisted/API SpecScene surfaces
// cannot retain spans from an earlier scene text version.
func RebaseSceneAnnotations(ann *scriptpkg.SceneAnnotations, text string) *scriptpkg.SceneAnnotations {
	return rebaseSceneAnnotations(ann, text)
}

// rebaseSceneAnnotations aligns provisional annotations with the final scene
// text. Extraction can precede scene synthesis, so spans produced from a
// segment hint must never be copied verbatim into the generated prose.
// Keyword spans that overlap a phrase or entity are removed to keep the
// annotation categories semantically distinct.
func rebaseSceneAnnotations(in *scriptpkg.SceneAnnotations, text string) *scriptpkg.SceneAnnotations {
	if in == nil {
		return nil
	}
	out := *in
	out.ImportantPhrases = nil
	out.ImportantWords = nil
	out.PrimaryEntities = nil
	out.SecondaryEntities = nil

	phraseSpans := make([]scriptpkg.AnnotationSpan, 0, len(in.ImportantPhrases))
	for _, span := range in.ImportantPhrases {
		if grounded, ok := findAnnotationSpan(text, span.Text); ok {
			span.StartRune, span.EndRune, span.Text = grounded.StartRune, grounded.EndRune, grounded.Text
			phraseSpans = append(phraseSpans, span)
		}
	}
	out.ImportantPhrases = phraseSpans

	entitySpans := make([]scriptpkg.AnnotationSpan, 0)
	rebaseEntity := func(entity scriptpkg.AnnotatedEntity) (scriptpkg.AnnotatedEntity, bool) {
		if strings.TrimSpace(entity.CanonicalName) == "" {
			entity.CanonicalName = strings.TrimSpace(entity.Text)
		}
		if strings.TrimSpace(entity.ID) == "" {
			entity.ID = "entity-" + safeAnnotationID(entity.CanonicalName)
		}
		mentions := append([]scriptpkg.AnnotationSpan(nil), entity.Mentions...)
		entity.Mentions = nil
		for _, mention := range mentions {
			if grounded, ok := findAnnotationSpan(text, mention.Text); ok {
				entity.Mentions = append(entity.Mentions, grounded)
			}
		}
		if len(entity.Mentions) == 0 {
			if grounded, ok := findAnnotationSpan(text, entity.Text); ok {
				entity.Mentions = []scriptpkg.AnnotationSpan{grounded}
			}
		}
		return entity, len(entity.Mentions) > 0
	}
	for _, entity := range in.PrimaryEntities {
		if grounded, ok := rebaseEntity(entity); ok {
			out.PrimaryEntities = append(out.PrimaryEntities, grounded)
			entitySpans = append(entitySpans, grounded.Mentions...)
		}
	}
	for _, entity := range in.SecondaryEntities {
		if grounded, ok := rebaseEntity(entity); ok {
			out.SecondaryEntities = append(out.SecondaryEntities, grounded)
			entitySpans = append(entitySpans, grounded.Mentions...)
		}
	}
	for _, word := range in.ImportantWords {
		grounded, ok := findAnnotationSpan(text, word.Text)
		if !ok || annotationSpanOverlapsAny(grounded, phraseSpans) || annotationSpanOverlapsAny(grounded, entitySpans) {
			continue
		}
		word.StartRune, word.EndRune, word.Text = grounded.StartRune, grounded.EndRune, grounded.Text
		key := strings.ToLower(strings.TrimSpace(word.Text))
		duplicate := false
		for _, existing := range out.ImportantWords {
			if strings.ToLower(strings.TrimSpace(existing.Text)) == key {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out.ImportantWords = append(out.ImportantWords, word)
		}
	}
	deduplicateGroundedEntities(&out)
	return &out
}

func deduplicateGroundedEntities(ann *scriptpkg.SceneAnnotations) {
	if ann == nil {
		return
	}
	keepLongest := func(in []scriptpkg.AnnotatedEntity) []scriptpkg.AnnotatedEntity {
		out := make([]scriptpkg.AnnotatedEntity, 0, len(in))
		for _, candidate := range in {
			replaced := false
			discard := false
			for i := range out {
				if out[i].Type != candidate.Type || !annotationSpansOverlap(out[i].Mentions, candidate.Mentions) {
					continue
				}
				if len([]rune(out[i].CanonicalName)) >= len([]rune(candidate.CanonicalName)) {
					discard = true
					break
				}
				out[i] = candidate
				replaced = true
				break
			}
			if !discard && !replaced {
				out = append(out, candidate)
			}
		}
		return out
	}
	ann.PrimaryEntities = keepLongest(ann.PrimaryEntities)
	secondary := keepLongest(ann.SecondaryEntities)
	filtered := secondary[:0]
	for _, candidate := range secondary {
		overlapsPrimary := false
		for _, primary := range ann.PrimaryEntities {
			if annotationSpansOverlap(primary.Mentions, candidate.Mentions) {
				overlapsPrimary = true
				break
			}
		}
		if !overlapsPrimary {
			filtered = append(filtered, candidate)
		}
	}
	ann.SecondaryEntities = filtered
}

func annotationSpansOverlap(left, right []scriptpkg.AnnotationSpan) bool {
	for _, a := range left {
		for _, b := range right {
			if a.StartRune < b.EndRune && b.StartRune < a.EndRune {
				return true
			}
		}
	}
	return false
}

func annotationSpanOverlapsAny(span scriptpkg.AnnotationSpan, others []scriptpkg.AnnotationSpan) bool {
	for _, other := range others {
		if span.StartRune < other.EndRune && other.StartRune < span.EndRune {
			return true
		}
	}
	return false
}

func findAnnotationSpan(text, value string) (scriptpkg.AnnotationSpan, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return scriptpkg.AnnotationSpan{}, false
	}
	runes, want := []rune(text), []rune(value)
	for start := 0; start+len(want) <= len(runes); start++ {
		match := true
		for i := range want {
			if runes[start+i] != want[i] {
				match = false
				break
			}
		}
		if match {
			return scriptpkg.AnnotationSpan{Text: string(want), StartRune: start, EndRune: start + len(want)}, true
		}
	}
	// Hints may differ only in capitalization or diacritics from the final
	// generated text (Cafe vs Cafe with an accent). Return the actual slice so
	// grounding and offsets remain canonical.
	for start := 0; start+len(want) <= len(runes); start++ {
		if equalsFoldAccents(string(runes[start:start+len(want)]), value) {
			return scriptpkg.AnnotationSpan{
				Text:      string(runes[start : start+len(want)]),
				StartRune: start,
				EndRune:   start + len(want),
			}, true
		}
	}
	return scriptpkg.AnnotationSpan{}, false
}

// equalsFoldAccents compares case-insensitively after folding common Latin
// diacritics. The comparison never changes the returned span text, so the
// document keeps its original spelling while the annotation stays grounded.
func equalsFoldAccents(left, right string) bool {
	a, b := []rune(left), []rune(right)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if foldAnnotationRune(a[i]) != foldAnnotationRune(b[i]) {
			return false
		}
	}
	return true
}

func foldAnnotationRune(r rune) rune {
	switch r {
	case 'à', 'á', 'â', 'ã', 'ä', 'å', 'À', 'Á', 'Â', 'Ã', 'Ä', 'Å':
		return 'a'
	case 'è', 'é', 'ê', 'ë', 'È', 'É', 'Ê', 'Ë':
		return 'e'
	case 'ì', 'í', 'î', 'ï', 'Ì', 'Í', 'Î', 'Ï':
		return 'i'
	case 'ò', 'ó', 'ô', 'õ', 'ö', 'Ò', 'Ó', 'Ô', 'Õ', 'Ö':
		return 'o'
	case 'ù', 'ú', 'û', 'ü', 'Ù', 'Ú', 'Û', 'Ü':
		return 'u'
	case 'ç', 'Ç':
		return 'c'
	case 'ñ', 'Ñ':
		return 'n'
	case 'ý', 'ÿ', 'Ý', 'Ÿ':
		return 'y'
	}
	return unicode.ToLower(r)
}

// normalizeAnnotationType maps an extracted entity type onto the canonical
// annotation kind through the single kernel registry
// (script.NormalizeAnnotationType). It is a thin alias so the batch merger
// and the runner projection share one taxonomy owner.
func normalizeAnnotationType(raw string) string {
	return scriptpkg.NormalizeAnnotationType(raw)
}

func safeAnnotationID(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
