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
		if span, ok := findAnnotationSpan(text, phrase); ok {
			span.ID = fmt.Sprintf("phrase-%s-%03d", safeAnnotationID(seg.SegmentID), i+1)
			span.Score = 0.80
			span.Kind = "key_statement"
			ann.ImportantPhrases = append(ann.ImportantPhrases, span)
		}
	}
	for i, word := range seg.Insights.ImportantWords {
		if span, ok := findAnnotationSpan(text, word); ok {
			span.Lemma = strings.ToLower(strings.TrimSpace(word))
			span.ID = fmt.Sprintf("word-%s-%03d", safeAnnotationID(seg.SegmentID), i+1)
			span.Score = float64(max(1, len(seg.Insights.ImportantWords)-i)) / float64(max(1, len(seg.Insights.ImportantWords)))
			ann.ImportantWords = append(ann.ImportantWords, span)
		}
	}
	seen := map[string]bool{}
	for _, entity := range seg.Insights.Entities {
		value := strings.TrimSpace(entity.Value)
		kind := normalizeAnnotationType(entity.Type)
		admitted := true
		if entity.EditorialDecision {
			value = strings.TrimSpace(entity.EditorialSurface)
			kind = normalizeAnnotationType(entity.EditorialType)
			if value == "" || entity.PromptEcho || kind == "" {
				continue
			}
			admitted = entity.VisualAdmission
		} else if resolvedKind := canonicalImageEntityType(seg, value); resolvedKind != "" {
			// The image-intent resolver has stronger typed identity grounding
			// than the baseline NER surface. Use its canonical family so a
			// place or venue is not projected as a PERSON portrait.
			kind = resolvedKind
		}
		kind = normalizeAnnotationType(kind)
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
			if resolved := canonicalImageEntityName(seg, canonical); resolved != "" {
				canonical = resolved
			}
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
			CanonicalName: canonical, Type: kind, Role: entity.Role,
			Confidence: entity.Confidence, Mentions: mentions,
			EditorialDecision: entity.EditorialDecision, EditorialSurface: entity.EditorialSurface,
			EditorialType: entity.EditorialType, PromptEcho: entity.PromptEcho, VisualAdmission: entity.VisualAdmission,
		}
		if kind == "LOGO" {
			if admitted && hasVerifiedBrandImage(canonical, seg) {
				item.Type = "LOGO"
			} else {
				item.Type = "BRAND"
			}
		} else if kind == "BRAND" && admitted && hasVerifiedBrandImage(canonical, seg) {
			item.Type = "LOGO"
		}
		// Carry BrainRust's linked identity through to the overlay media index.
		item.CanonicalEntityID = seg.ResolverCanonicalID(canonical, value)
		if item.CanonicalEntityID == "" && scriptpkg.NormalizeAnnotationType(kind) == "LOGO" {
			item.CanonicalEntityID = "logo:" + normalizeBrandAssetName(canonical)
		}
		primary, hasRole := scriptpkg.AnnotationRoleIsPrimary(entity.Role)
		if !hasRole {
			// Pre-v3 producers did not carry a role. Keep the legacy kind
			// fallback for them; v3 always supplies the Rust decision.
			primary = scriptpkg.IsAnnotationEntityKind(item.Type)
		}
		if primary {
			ann.PrimaryEntities = append(ann.PrimaryEntities, item)
		} else {
			ann.SecondaryEntities = append(ann.SecondaryEntities, item)
		}
	}
	appendGroundedNumberEntities(ann, text)
	return rebaseSceneAnnotations(ann, text)
}

func hasVerifiedBrandImage(name string, seg scriptpkg.VidRushSegmentResult) bool {
	want := normalizeBrandAssetName(name)
	if want == "" {
		return false
	}
	candidates := append(append([]scriptpkg.SegmentAssetCandidate(nil), seg.Assets.Candidates...), seg.Assets.SecondaryImages...)
	for _, candidate := range candidates {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) ||
			candidate.AcquisitionStatus != scriptpkg.VidRushStatusAcquired ||
			candidate.VerificationStatus != scriptpkg.VidRushStatusVerified ||
			candidate.PersistenceStatus != scriptpkg.VidRushStatusPersisted ||
			!strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), "accepted") ||
			strings.TrimSpace(candidate.DriveLink) == "" || strings.TrimSpace(candidate.ContentSHA256) == "" ||
			strings.EqualFold(strings.TrimSpace(candidate.IndexStatus), "failed") ||
			strings.EqualFold(strings.TrimSpace(candidate.RightsStatus), "rejected") {
			continue
		}
		if normalizeBrandAssetName(candidate.Entity) == want {
			return true
		}
	}
	return false
}

func normalizeBrandAssetName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " "))
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
	matches := make(map[string]string)
	for _, candidate := range capitalizedNameRE.FindAllString(text, -1) {
		candidate = strings.TrimSpace(candidate)
		if strings.EqualFold(candidate, value) || strings.Contains(strings.ToLower(candidate), strings.ToLower(value)) {
			matches[strings.ToLower(candidate)] = candidate
		}
	}
	// A surname shared by two people is ambiguous: expanding it to the first
	// capitalized window invented an identity the text never established.
	// Stay on the extracted surface and let the canonical resolver decide.
	if len(matches) != 1 {
		return value
	}
	for _, candidate := range matches {
		return candidate
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
