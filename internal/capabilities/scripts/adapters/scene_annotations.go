package adapters

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VidRushSceneMerger projects one immutable VidRush segment result onto its
// matching scene through the canonical sceneAnnotations projection. It
// implements the capability's SegmentSceneMerger port so the incremental
// reducer and the non-streaming batch path produce identical annotations from
// the same segment insights. Binding projection remains the responsibility of
// the dedicated binding/visual-planning processors; this merger owns only the
// per-scene semantic annotation merge.
type VidRushSceneMerger struct {
	language string
}

// NewVidRushSceneMerger constructs a merger for the given scene language
// (ISO 639-1). Empty language falls back to "und" inside sceneAnnotations.
func NewVidRushSceneMerger(language string) *VidRushSceneMerger {
	return &VidRushSceneMerger{language: strings.TrimSpace(language)}
}

// Merge returns a new scene with the segment's annotations applied. The input
// scene is never mutated; all other scene fields are carried forward as-is.
func (m *VidRushSceneMerger) Merge(scene scriptpkg.SpecScene, result scriptpkg.VidRushSegmentResult) scriptpkg.SpecScene {
	out := scene
	if m == nil {
		return out
	}
	out.Annotations = sceneAnnotations(scene.Text, m.language, result)
	return out
}

func sceneAnnotations(text, language string, seg scriptpkg.VidRushSegmentResult) *scriptpkg.SceneAnnotations {
	ann := &scriptpkg.SceneAnnotations{Version: 1, Language: strings.TrimSpace(language), Status: "completed"}
	if ann.Language == "" {
		ann.Language = "und"
	}
	for i, phrase := range seg.Insights.ImportantPhrases {
		if i > 0 {
			break
		} // contract: at most one important phrase per scene
		if span, ok := findAnnotationSpan(text, phrase); ok {
			span.ID = fmt.Sprintf("phrase-%s-001", safeAnnotationID(seg.SegmentID))
			span.Score = 0.80
			span.Kind = "key_statement"
			ann.ImportantPhrases = append(ann.ImportantPhrases, span)
		}
	}
	for i, word := range seg.Insights.ImportantWords {
		if span, ok := findAnnotationSpan(text, word); ok {
			span.Lemma = strings.ToLower(strings.TrimSpace(word))
			span.ID = fmt.Sprintf("word-%s-%03d", safeAnnotationID(seg.SegmentID), i+1)
			span.Score = float64(maxInt(1, len(seg.Insights.ImportantWords)-i)) / float64(maxInt(1, len(seg.Insights.ImportantWords)))
			ann.ImportantWords = append(ann.ImportantWords, span)
		}
	}
	seen := map[string]bool{}
	for _, entity := range seg.Insights.Entities {
		value := strings.TrimSpace(entity.Value)
		kind := normalizeAnnotationType(entity.Type)
		if kind == "KEYWORD" || kind == "VISUAL_SUBJECT" {
			// KEYWORD / VISUAL_SUBJECT are search/index surfaces, never
			// spoken entities (mirror of the runner projection).
			continue
		}
		if value == "" {
			continue
		}
		span, ok := findAnnotationSpan(text, value)
		if !ok {
			continue
		}
		canonical := value
		if kind == "PERSON" {
			canonical = expandPersonCanonicalName(text, value)
		}
		// CONCEPT is an NLP hint, not a renderable entity type. A grounded
		// multi-word concept is promoted to the editorial emphasis surface so
		// it converges with phrases through the single overlay planner.
		if kind == "CONCEPT" && len(strings.Fields(value)) >= 2 {
			if len(ann.ImportantPhrases) == 0 {
				phrase := span
				phrase.ID = fmt.Sprintf("phrase-%s-concept", safeAnnotationID(seg.SegmentID))
				phrase.Score = 0.86
				phrase.Kind = "IMPORTANT_PHRASE"
				ann.ImportantPhrases = append(ann.ImportantPhrases, phrase)
			}
			continue
		}
		entityKey := kind + "\x00" + strings.ToLower(canonical)
		if seen[entityKey] {
			continue
		}
		seen[entityKey] = true
		mentions := findAllAnnotationSpans(text, canonical)
		if len(mentions) == 0 {
			mentions = []scriptpkg.AnnotationSpan{span}
		}
		if kind == "PERSON" {
			// A surname-only occurrence is a mention of the same person,
			// never a second canonical entity.
			mentions = appendDistinctPersonAliasMentions(text, canonical, mentions)
		}
		item := scriptpkg.AnnotatedEntity{
			ID: "entity-" + safeAnnotationID(canonical), Text: canonical,
			CanonicalName: canonical, Type: kind, Confidence: entity.Confidence,
			Mentions: mentions,
		}
		// Stamp the canonical_entity_id the Image Search Intent resolver
		// chose for this entity (the join key of the overlay media index), so
		// the overlay compile resolves the card asset under the SAME identity
		// — never a re-derivation from a possibly-different surface.
		item.CanonicalEntityID = seg.ResolverCanonicalID(canonical, value)
		if scriptpkg.IsAnnotationEntityKind(kind) {
			ann.PrimaryEntities = append(ann.PrimaryEntities, item)
		} else {
			ann.SecondaryEntities = append(ann.SecondaryEntities, item)
		}
	}
	return rebaseSceneAnnotations(ann, text)
}

// RebaseSceneAnnotations is the final output-boundary normalization. It is
// exported for the use-case finalizer so persisted/API SpecScene surfaces
// cannot retain spans from an earlier scene text version.
func RebaseSceneAnnotations(ann *scriptpkg.SceneAnnotations, text string) *scriptpkg.SceneAnnotations {
	return rebaseSceneAnnotations(ann, text)
}

var capitalizedNameRE = regexp.MustCompile(`(?:\b[\p{Lu}][\p{L}'’-]+)(?:\s+[\p{Lu}][\p{L}'’-]+)+`)

func expandPersonCanonicalName(text, value string) string {
	value = strings.TrimSpace(value)
	if len(strings.Fields(value)) > 1 {
		return value
	}
	for _, candidate := range capitalizedNameRE.FindAllString(text, -1) {
		if strings.EqualFold(candidate, value) || strings.Contains(strings.ToLower(candidate), strings.ToLower(value)) {
			return strings.TrimSpace(candidate)
		}
	}
	return value
}

func findAllAnnotationSpans(text, value string) []scriptpkg.AnnotationSpan {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	runes, want := []rune(text), []rune(value)
	if len(want) == 0 {
		return nil
	}
	spans := make([]scriptpkg.AnnotationSpan, 0, 2)
	for start := 0; start+len(want) <= len(runes); start++ {
		if strings.EqualFold(string(runes[start:start+len(want)]), value) {
			spans = append(spans, scriptpkg.AnnotationSpan{Text: string(runes[start : start+len(want)]), StartRune: start, EndRune: start + len(want)})
		}
	}
	return spans
}

func appendDistinctPersonAliasMentions(text, canonical string, mentions []scriptpkg.AnnotationSpan) []scriptpkg.AnnotationSpan {
	parts := strings.Fields(canonical)
	if len(parts) < 2 {
		return mentions
	}
	alias := parts[len(parts)-1]
	for _, span := range findAllAnnotationSpans(text, alias) {
		if span.StartRune > 0 && unicode.IsLetter([]rune(text)[span.StartRune-1]) {
			continue
		}
		if annotationSpanOverlapsAny(span, mentions) {
			continue
		}
		mentions = append(mentions, span)
	}
	return mentions
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
	if len(phraseSpans) == 0 && strings.TrimSpace(text) != "" {
		if phrase := bestGroundedAnnotationSentence(text); phrase != "" {
			span, _ := findAnnotationSpan(text, phrase)
			span.ID = fmt.Sprintf("phrase-final-%03d", 1)
			span.Score = 0.80
			span.Kind = "key_statement"
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
	for _, entity := range discoverGroundedEntities(text) {
		canonical := entity.CanonicalName
		duplicate := false
		for _, existing := range append(out.PrimaryEntities, out.SecondaryEntities...) {
			if strings.EqualFold(existing.CanonicalName, canonical) && existing.Type == entity.Type {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		if scriptpkg.IsAnnotationEntityKind(entity.Type) {
			out.PrimaryEntities = append(out.PrimaryEntities, entity)
		} else {
			out.SecondaryEntities = append(out.SecondaryEntities, entity)
		}
		entitySpans = append(entitySpans, entity.Mentions...)
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
	if len(out.ImportantWords) == 0 {
		out.ImportantWords = fallbackDistinctKeywords(text, phraseSpans, entitySpans, out.Language)
	}
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

func bestGroundedAnnotationSentence(text string) string {
	best, bestScore := "", -1
	for _, raw := range regexp.MustCompile(`[^.!?]+(?:[.!?]+|$)`).FindAllString(text, -1) {
		candidate := strings.TrimSpace(raw)
		words := strings.Fields(candidate)
		if len(words) == 0 {
			continue
		}
		score := len(words)
		if capitalizedNameRE.MatchString(candidate) {
			score += 8
		}
		if score > bestScore {
			best, bestScore = candidate, score
		}
	}
	return best
}

func discoverGroundedEntities(text string) []scriptpkg.AnnotatedEntity {
	values := make([]struct{ value, kind string }, 0)
	seen := map[string]bool{}
	add := func(value, kind string) {
		key := kind + "\x00" + strings.ToLower(strings.TrimSpace(value))
		if strings.TrimSpace(value) == "" || seen[key] {
			return
		}
		seen[key] = true
		values = append(values, struct{ value, kind string }{value, kind})
	}
	for _, value := range capitalizedNameRE.FindAllString(text, -1) {
		add(strings.TrimSpace(value), classifyDiscoveredEntity(value))
	}
	for _, value := range regexp.MustCompile(`\b[A-Z]{2,8}\b`).FindAllString(text, -1) {
		add(value, "ORG")
	}
	for _, value := range regexp.MustCompile(`\b(?:1[89]\d{2}|20\d{2})\b`).FindAllString(text, -1) {
		add(value, "DATE")
	}
	result := make([]scriptpkg.AnnotatedEntity, 0, len(values))
	for _, item := range values {
		mentions := findAllAnnotationSpans(text, item.value)
		if len(mentions) == 0 {
			continue
		}
		canonical := item.value
		if item.kind == "PERSON" {
			mentions = appendDistinctPersonAliasMentions(text, canonical, mentions)
		}
		result = append(result, scriptpkg.AnnotatedEntity{
			ID: "entity-" + safeAnnotationID(canonical), Text: canonical,
			CanonicalName: canonical, Type: item.kind, Confidence: 0.85,
			Mentions: mentions,
		})
	}
	return result
}

func classifyDiscoveredEntity(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "las vegas" || lower == "new york" || lower == "los angeles" || lower == "roma" || lower == "parigi" || lower == "londra" {
		return "GPE"
	}
	return "PERSON"
}

func fallbackDistinctKeywords(text string, phrases, entities []scriptpkg.AnnotationSpan, language string) []scriptpkg.AnnotationSpan {
	runes := []rune(text)
	result := make([]scriptpkg.AnnotationSpan, 0, 3)
	seen := map[string]bool{}
	for i := 0; i < len(runes) && len(result) < 3; {
		for i < len(runes) && !unicode.IsLetter(runes[i]) {
			i++
		}
		start := i
		for i < len(runes) && (unicode.IsLetter(runes[i]) || runes[i] == '\'' || runes[i] == '’') {
			i++
		}
		if start == i {
			continue
		}
		value := strings.Trim(string(runes[start:i]), "'’")
		key := strings.ToLower(value)
		if len([]rune(value)) < 6 {
			continue
		}
		if seen[key] {
			continue
		}
		span := scriptpkg.AnnotationSpan{Text: value, StartRune: start, EndRune: i}
		if annotationSpanOverlapsAny(span, phrases) || annotationSpanOverlapsAny(span, entities) {
			continue
		}
		seen[key] = true
		span.Lemma = key
		span.Score = float64(3-len(result)) / 3
		result = append(result, span)
	}
	return result
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
	// Hints may differ only in capitalization from the final generated text.
	// Return the actual text slice so grounding and offsets remain canonical.
	for start := 0; start+len(want) <= len(runes); start++ {
		if strings.EqualFold(string(runes[start:start+len(want)]), value) {
			return scriptpkg.AnnotationSpan{
				Text:      string(runes[start : start+len(want)]),
				StartRune: start,
				EndRune:   start + len(want),
			}, true
		}
	}
	return scriptpkg.AnnotationSpan{}, false
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
