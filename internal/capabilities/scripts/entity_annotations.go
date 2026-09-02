// Package scriptgeneration — entity_annotations.go owns the deterministic
// projection of a scene's VidRush segment entities onto the durable
// Scene.Annotations surface. It mirrors the batch path's sceneAnnotations
// classification (PERSON/ORG/GPE → primary, everything else → secondary) so
// the durable runner and the legacy batch flow produce the same per-scene
// annotation contract from the same segment insights.
package scriptgeneration

import (
	"net/url"
	"strings"
	"unicode"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// projectEntityAnnotations builds the scene-local semantic annotations from a
// single VidRush segment result. Entities are grounded in the scene text
// (rune offsets); an entity whose value never occurs verbatim in the text is
// skipped, never faked. Returns nil when the segment produced no grounded
// entity.
func projectEntityAnnotations(text, language string, seg scriptpkg.VidRushSegmentResult) *scriptpkg.SceneAnnotations {
	ann := &scriptpkg.SceneAnnotations{Version: 1, Language: strings.TrimSpace(language), Status: "completed"}
	if ann.Language == "" {
		ann.Language = "und"
	}
	// ── Important phrases / words (mirror of the legacy batch adapter) ─────
	// The contract is at most ONE important phrase per scene; words keep the
	// legacy descending score (first word wins). Both are grounded in the
	// scene text like entities: a phrase/word that never occurs verbatim is
	// skipped, never faked. They are hints for the overlay planner, NOT
	// spoken-entity facts: the entity timeline WORD gate never reads them.
	for i, phrase := range seg.Insights.ImportantPhrases {
		if i > 0 {
			break
		} // contract: at most one important phrase per scene
		if span, ok := findEntitySpan(text, phrase); ok {
			ann.ImportantPhrases = append(ann.ImportantPhrases, scriptpkg.AnnotationSpan{
				Text: span.Text, StartRune: span.StartRune, EndRune: span.EndRune,
				Score: 0.80, Kind: "key_statement",
			})
		}
	}
	for i, word := range seg.Insights.ImportantWords {
		if span, ok := findEntitySpan(text, word); ok {
			score := float64(maxInt(1, len(seg.Insights.ImportantWords)-i)) / float64(maxInt(1, len(seg.Insights.ImportantWords)))
			ann.ImportantWords = append(ann.ImportantWords, scriptpkg.AnnotationSpan{
				Text: span.Text, Lemma: strings.ToLower(strings.TrimSpace(word)),
				StartRune: span.StartRune, EndRune: span.EndRune,
				Score: score, Kind: "IMPORTANT_WORD",
			})
		}
	}
	seen := map[string]bool{}
	for _, entity := range seg.Insights.Entities {
		value := strings.TrimSpace(entity.Value)
		if value == "" {
			continue
		}
		kind := normalizeEntityAnnotationType(entity.Type)
		if kind == "KEYWORD" || kind == "VISUAL_SUBJECT" {
			// KEYWORD and VISUAL_SUBJECT are not spoken entities: they
			// describe search/index surface and visual subjects, never a
			// name the voiceover must speak verbatim. Projecting them into
			// Annotations would make the entity timeline WORD gate demand a
			// phrase the narration never said (e.g. "PERSON").
			continue
		}
		span, ok := findEntitySpan(text, value)
		if !ok {
			continue
		}
		canonical := value
		if kind == "PERSON" {
			canonical = expandPersonName(text, value)
		}
		// A multi-word CONCEPT is editorial emphasis, not a second NER
		// entity. Keep it source-grounded and feed the canonical phrase
		// planner; single-word concepts remain secondary semantic entities.
		if kind == "CONCEPT" && len(strings.Fields(value)) >= 2 {
			if len(ann.ImportantPhrases) == 0 {
				ann.ImportantPhrases = append(ann.ImportantPhrases, scriptpkg.AnnotationSpan{
					Text: span.Text, StartRune: span.StartRune, EndRune: span.EndRune,
					Score: 0.86, Kind: "IMPORTANT_PHRASE",
				})
			}
			continue
		}
		key := kind + "\x00" + strings.ToLower(canonical)
		if seen[key] {
			continue
		}
		seen[key] = true
		mentions := findAllEntitySpans(text, canonical)
		if len(mentions) == 0 {
			mentions = []scriptpkg.AnnotationSpan{span}
		}
		item := scriptpkg.AnnotatedEntity{
			ID: "entity-" + safeEntityID(canonical), Text: canonical,
			CanonicalName: canonical, Type: kind, Confidence: entity.Confidence,
			Mentions: mentions,
		}
		if kind == "PERSON" {
			item.Image = entityImageBindingFor(canonical, seg)
		}
		if kind == "PERSON" || kind == "ORG" || kind == "GPE" {
			ann.PrimaryEntities = append(ann.PrimaryEntities, item)
		} else {
			ann.SecondaryEntities = append(ann.SecondaryEntities, item)
		}
	}
	if len(ann.PrimaryEntities)+len(ann.SecondaryEntities)+len(ann.ImportantPhrases)+len(ann.ImportantWords) == 0 {
		return nil
	}
	return ann
}

// entityImageBindingFor projects only a durable, identity-scoped internet
// image onto a PERSON annotation. Generic scene candidates and remote-only
// results must never become a person's image.
func entityImageBindingFor(name string, seg scriptpkg.VidRushSegmentResult) *scriptpkg.EntityImageBinding {
	want := normalizeEntityImageName(name)
	if want == "" {
		return nil
	}
	all := append(append([]scriptpkg.SegmentAssetCandidate(nil), seg.Assets.Candidates...), seg.Assets.SecondaryImages...)
	for _, candidate := range all {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) ||
			strings.TrimSpace(candidate.DriveLink) == "" || strings.TrimSpace(candidate.LegacyFileMD5) == "" ||
			candidate.AcquisitionStatus != scriptpkg.VidRushStatusAcquired ||
			candidate.VerificationStatus != scriptpkg.VidRushStatusVerified ||
			candidate.PersistenceStatus != scriptpkg.VidRushStatusPersisted ||
			strings.EqualFold(strings.TrimSpace(candidate.IndexStatus), "failed") ||
			strings.EqualFold(strings.TrimSpace(candidate.RightsStatus), "rejected") {
			continue
		}
		query := normalizeEntityImageName(candidate.Query)
		if query != want && !strings.Contains(query, want) {
			continue
		}
		return &scriptpkg.EntityImageBinding{
			Status: "resolved", AssetID: candidate.AssetID, DriveLink: candidate.DriveLink,
			Source: candidate.Provider, License: candidate.RightsBasis,
			PreviewURL: entityImagePreviewURL(candidate), SHA256: candidate.LegacyFileMD5,
		}
	}
	return nil
}

func normalizeEntityImageName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(strings.TrimSuffix(value, "'s"), "’s")
	return strings.Join(strings.Fields(value), " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func entityImagePreviewURL(candidate scriptpkg.SegmentAssetCandidate) string {
	if id := driveFileID(candidate.DriveLink); id != "" {
		return "https://drive.google.com/uc?export=download&id=" + url.QueryEscape(id)
	}
	return firstNonEmpty(candidate.SourceURL, candidate.PreviewURL)
}

func driveFileID(link string) string {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return ""
	}
	if id := parsed.Query().Get("id"); id != "" {
		return id
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := range parts {
		if (parts[i] == "d" || parts[i] == "file") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// maxInt returns the larger of the two integers (mirror of the legacy batch
// adapter helper used by the important-word score projection).
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// normalizeEntityAnnotationType maps an extracted entity type onto the
// canonical annotation kind, mirroring the batch adapter classification.
func normalizeEntityAnnotationType(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "PERSON":
		return "PERSON"
	case "ORG", "ORGANIZATION", "COMPANY", "CORP", "CORPORATION", "BUSINESS":
		return "ORG"
	case "GPE", "PLACE", "LOCATION", "CITY", "COUNTRY":
		return "GPE"
	case "DATE":
		return "DATE"
	case "TIME":
		return "TIME"
	case "CARDINAL":
		return "CARDINAL"
	case "NUMBER", "NUM":
		return "NUMBER"
	case "ORDINAL":
		return "ORDINAL"
	case "MONEY":
		return "MONEY"
	case "PERCENT":
		return "PERCENT"
	case "QUOTE":
		return "QUOTE"
	case "PRODUCT":
		return "PRODUCT"
	case "LOGO":
		return "LOGO"
	case "EVENT":
		return "EVENT"
	case "WORK_OF_ART", "WORK":
		return "WORK_OF_ART"
	case "KEYWORD":
		return "KEYWORD"
	case "VISUAL_SUBJECT":
		return "VISUAL_SUBJECT"
	default:
		return "CONCEPT"
	}
}

// findEntitySpan locates the first case-sensitive (then case-insensitive)
// occurrence of value in text and returns its rune span.
func findEntitySpan(text, value string) (scriptpkg.AnnotationSpan, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return scriptpkg.AnnotationSpan{}, false
	}
	runes, want := []rune(text), []rune(value)
	for start := 0; start+len(want) <= len(runes); start++ {
		if string(runes[start:start+len(want)]) == value {
			return scriptpkg.AnnotationSpan{Text: string(runes[start : start+len(want)]), StartRune: start, EndRune: start + len(want)}, true
		}
	}
	for start := 0; start+len(want) <= len(runes); start++ {
		if strings.EqualFold(string(runes[start:start+len(want)]), value) {
			return scriptpkg.AnnotationSpan{Text: string(runes[start : start+len(want)]), StartRune: start, EndRune: start + len(want)}, true
		}
	}
	return scriptpkg.AnnotationSpan{}, false
}

// findAllEntitySpans returns every case-insensitive occurrence of value in
// text as rune spans.
func findAllEntitySpans(text, value string) []scriptpkg.AnnotationSpan {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	runes, want := []rune(text), []rune(value)
	if len(want) == 0 {
		return nil
	}
	var spans []scriptpkg.AnnotationSpan
	for start := 0; start+len(want) <= len(runes); start++ {
		if strings.EqualFold(string(runes[start:start+len(want)]), value) {
			spans = append(spans, scriptpkg.AnnotationSpan{Text: string(runes[start : start+len(want)]), StartRune: start, EndRune: start + len(want)})
		}
	}
	return spans
}

// expandPersonName expands a surname-only entity value to the full capitalized
// name present in the text (e.g. "Johnson" → "Dwayne Johnson").
func expandPersonName(text, value string) string {
	value = strings.TrimSpace(value)
	if len(strings.Fields(value)) > 1 {
		return value
	}
	var candidates []string
	var current strings.Builder
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if unicode.IsUpper(r) || r == '\'' || r == '’' || r == '-' || (unicode.IsLetter(r) && current.Len() > 0) {
			current.WriteRune(r)
		} else if current.Len() > 0 {
			candidates = append(candidates, strings.TrimSpace(current.String()))
			current.Reset()
		}
	}
	if current.Len() > 0 {
		candidates = append(candidates, strings.TrimSpace(current.String()))
	}
	for _, candidate := range candidates {
		fields := strings.Fields(candidate)
		if len(fields) < 2 {
			continue
		}
		for _, field := range fields {
			if strings.EqualFold(field, value) {
				return candidate
			}
		}
	}
	return value
}

// applySegmentEntityAnnotations projects each segment's grounded entities onto
// the matching scene's Annotations. Matching uses the canonical identity
// precedence (SceneID, then Position); scenes without a matching segment keep
// their nil Annotations. It mutates result.Scenes in place and never invents
// annotations for a segment whose entities do not occur in the scene text.
func applySegmentEntityAnnotations(result *GenerateResult, language Language, segments []scriptpkg.VidRushSegmentResult) {
	if result == nil {
		return
	}
	bySceneID := make(map[string]int, len(result.Scenes))
	for i := range result.Scenes {
		bySceneID[result.Scenes[i].ID] = i
	}
	for _, seg := range segments {
		idx, ok := bySceneID[seg.SceneID]
		if !ok && seg.SceneID == "" {
			// Fall back to position matching when the segment carries no scene
			// identity (legacy barriers).
			if seg.Position >= 0 && seg.Position < len(result.Scenes) {
				idx, ok = seg.Position, true
			}
		}
		if !ok {
			continue
		}
		text := strings.TrimSpace(result.Scenes[idx].Text[language])
		if text == "" {
			continue
		}
		if ann := projectEntityAnnotations(text, string(language), seg); ann != nil {
			result.Scenes[idx].Annotations = ann
		}
	}
}

// applySegmentEntityResults projects each segment's typed entities onto the
// matching scene's canonical per-scene EntityResult surface (the same
// EntityResult model as the document aggregate — no second entity model). It
// mirrors applySegmentEntityAnnotations matching (SceneID, then Position). A
// scene that legitimately carries no entities keeps an explicit empty result
// with EntityOverlayRequired=false; an entity is never invented.
func applySegmentEntityResults(result *GenerateResult, segments []scriptpkg.VidRushSegmentResult) {
	if result == nil {
		return
	}
	bySceneID := make(map[string]int, len(result.Scenes))
	for i := range result.Scenes {
		bySceneID[result.Scenes[i].ID] = i
	}
	for _, seg := range segments {
		idx, ok := bySceneID[seg.SceneID]
		if !ok && seg.SceneID == "" {
			if seg.Position >= 0 && seg.Position < len(result.Scenes) {
				idx, ok = seg.Position, true
			}
		}
		if !ok {
			continue
		}
		res := projectSceneEntityResult(seg)
		result.Scenes[idx].Entities = res
		result.Scenes[idx].EntityOverlayRequired = entityResultHasValues(res)
	}
}

// projectSceneEntityResult builds the per-scene typed EntityResult from one
// segment's extracted entities, using the same classification as the
// aggregate projection (PERSON → persons; LOCATION/PLACE/COUNTRY/CITY →
// places; every other type → concepts). It returns an explicit empty result
// (never nil) so a scene with no entities is represented as entities=[] with
// entity_overlay_required=false — no entity is invented.
func projectSceneEntityResult(seg scriptpkg.VidRushSegmentResult) *scriptpkg.EntityResult {
	res := &scriptpkg.EntityResult{
		Persons:          []scriptpkg.Entity{},
		Places:           []scriptpkg.Entity{},
		Concepts:         []scriptpkg.Entity{},
		ImportantPhrases: append([]string(nil), seg.Insights.ImportantPhrases...),
		ImportantWords:   append([]string(nil), seg.Insights.ImportantWords...),
	}
	for _, ent := range seg.Insights.Entities {
		value := strings.TrimSpace(ent.Value)
		if value == "" {
			continue
		}
		entity := scriptpkg.Entity{Value: value, Type: ent.Type, Score: float32(ent.Confidence)}
		switch strings.ToUpper(strings.TrimSpace(ent.Type)) {
		case "PERSON":
			res.Persons = append(res.Persons, entity)
		case "LOCATION", "PLACE", "COUNTRY", "CITY":
			res.Places = append(res.Places, entity)
		default:
			res.Concepts = append(res.Concepts, entity)
		}
	}
	return res
}

// entityResultHasValues reports whether a per-scene EntityResult carries at
// least one typed entity value (the entity_overlay_required signal). It
// never invents an entity: an empty result is false.
func entityResultHasValues(res *scriptpkg.EntityResult) bool {
	if res == nil {
		return false
	}
	return len(res.Persons)+len(res.Places)+len(res.Concepts) > 0
}

// safeEntityID normalizes a value into a lowercase alphanumeric ID.
func safeEntityID(value string) string {
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
