// Package semantics owns the PURE projections over the visual-entity wire
// contract: locating the rune span of a grounded surface in source text,
// grounding phrases against entity spans, and ranking/filtering entities for
// translation and image search.
//
// Every function here is receiver-free and I/O-free, so both the scripts root
// and the vidrush package can share them without either depending on the other.
// The value itself lives in scripts/contract.VisualEntity.
package semantics

import (
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// LimitStrings truncates values to at most limit entries, copying the result.
func LimitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]string(nil), values...)
}

// FindEntitySpan locates the first case-sensitive (then case-insensitive)
// occurrence of value in text and returns its rune span.
//
// The exact pass is delegated to the stdlib. The case-insensitive pass walks
// rune-aligned byte offsets and compares zero-copy substrings with
// strings.EqualFold: the previous implementation materialised a []rune copy of
// the WHOLE text and built a fresh string per candidate position, which on a
// per-scene call path was an O(len(text)) allocation storm (and is invoked
// once per phrase and once per entity by the phrase grounding gate below).
func FindEntitySpan(text, value string) (scriptpkg.AnnotationSpan, bool) {
	value = strings.TrimSpace(value)
	if value == "" || text == "" {
		return scriptpkg.AnnotationSpan{}, false
	}
	if idx := strings.Index(text, value); idx >= 0 {
		startRune := utf8.RuneCountInString(text[:idx])
		return scriptpkg.AnnotationSpan{
			Text:      text[idx : idx+len(value)],
			StartRune: startRune,
			EndRune:   startRune + utf8.RuneCountInString(value),
		}, true
	}
	wantRunes := utf8.RuneCountInString(value)
	if wantRunes == 0 {
		return scriptpkg.AnnotationSpan{}, false
	}
	startRune := 0
	for start := 0; start < len(text); {
		if end := AdvanceRunes(text, start, wantRunes); end > start && strings.EqualFold(text[start:end], value) {
			return scriptpkg.AnnotationSpan{
				Text:      text[start:end],
				StartRune: startRune,
				EndRune:   startRune + wantRunes,
			}, true
		}
		_, size := utf8.DecodeRuneInString(text[start:])
		if size <= 0 {
			break
		}
		start += size
		startRune++
	}
	return scriptpkg.AnnotationSpan{}, false
}

// AdvanceRunes returns the byte offset n runes after start, or -1 when the
// string ends first. It lets a caller compare a fixed-rune-length window
// without allocating a rune slice for the text.
func AdvanceRunes(text string, start, n int) int {
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

func GroundImportantPhrases(source string, entities []contract.VisualEntity, phrases []string, limit int) []string {
	if limit <= 0 {
		limit = len(phrases)
	}
	out := make([]string, 0, min(limit, len(phrases)))
	seen := make(map[string]struct{}, len(phrases))

	// Entity spans do not depend on the phrase being tested, so they are
	// resolved ONCE per segment instead of once per (phrase, entity) pair. The
	// previous nested resolution made this gate O(phrases × entities × source
	// length) on every scene.
	entitySpans := make([]scriptpkg.AnnotationSpan, 0, len(entities))
	for _, entity := range entities {
		if entitySpan, entityOK := FindEntitySpan(source, entity.Text); entityOK {
			entitySpans = append(entitySpans, entitySpan)
		}
	}

	for _, phrase := range phrases {
		phrase = strings.TrimSpace(phrase)
		if len(strings.Fields(phrase)) < 2 {
			continue
		}
		// The proper-name filter (containsProperNamePair) is applied
		// exactly once per candidate source: phrases.Select owns the
		// selector-derived candidates, and the operator-supplied
		// MediaExtractionPolicy hints are filtered at their ingress in
		// vidrush_semantic_chain.go. Re-checking here would validate the same
		// selector output a second time.
		span, ok := FindEntitySpan(source, phrase)
		if !ok {
			continue
		}
		// A phrase is editorial text, not an entity-bearing label. Reject
		// spans that overlap any extracted named entity, including
		// partial forms such as "LeBron James and Johann" or "Sebastian
		// Bach". Those belong to the entity surface only.
		phraseOverlapsEntity := false
		for _, entitySpan := range entitySpans {
			if span.StartRune < entitySpan.EndRune && entitySpan.StartRune < span.EndRune {
				phraseOverlapsEntity = true
				break
			}
		}
		if phraseOverlapsEntity {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(span.Text))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, span.Text)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// EntityRuneSpans resolves the RUNE spans of the grounded entity surfaces in
// source. The phrase selector consumes these as its blocked ranges, so no
// phrase can cover a name the entity overlays own. Grounding stays here, with
// FindEntitySpan and the VisualEntity contract, which keeps the selector a
// leaf package with a neutral input instead of a second entity model.
func EntityRuneSpans(source string, entities []contract.VisualEntity) [][2]int {
	spans := make([][2]int, 0, len(entities))
	for _, entity := range entities {
		if span, ok := FindEntitySpan(source, entity.Text); ok {
			spans = append(spans, [2]int{span.StartRune, span.EndRune})
		}
	}
	return spans
}

func TranslatedSpecialNames(text string, candidates []string, entities []contract.VisualEntity, limit int) []string {
	var out []string
	seen := make(map[string]struct{})
	appendName := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	// A special name must correspond to a typed, source-grounded entity.
	// This also replaces partial model spans ("Las", "Vegas") with the
	// complete localized entity and rejects German common nouns that were
	// surfaced by capitalization-only fallback.
	for _, entity := range entities {
		if !IsNamedVisualEntity(entity.Type) && entity.Type != scriptpkg.EntityTypeProduct && !strings.EqualFold(string(entity.Type), "BRAND") && !strings.EqualFold(string(entity.Type), "LOGO") {
			continue
		}
		if span, ok := FindEntitySpan(text, entity.Text); ok {
			appendName(span.Text)
		}
	}
	// Keep model candidates only when they overlap a typed entity. The entity
	// surface is emitted above, so candidates never introduce partial aliases.
	for _, candidate := range candidates {
		span, ok := FindEntitySpan(text, candidate)
		if !ok {
			continue
		}
		for _, entity := range entities {
			if !IsNamedVisualEntity(entity.Type) && entity.Type != scriptpkg.EntityTypeProduct && !strings.EqualFold(string(entity.Type), "BRAND") && !strings.EqualFold(string(entity.Type), "LOGO") {
				continue
			}
			entitySpan, entityOK := FindEntitySpan(text, entity.Text)
			if entityOK && span.StartRune < entitySpan.EndRune && entitySpan.StartRune < span.EndRune {
				appendName(entitySpan.Text)
				break
			}
		}
	}
	return LimitStrings(out, limit)
}

func IsNamedVisualEntity(kind scriptpkg.EntityType) bool {
	switch kind {
	case scriptpkg.EntityTypePerson, scriptpkg.EntityTypeLocation, scriptpkg.EntityTypeOrganization,
		scriptpkg.EntityTypeEvent, scriptpkg.EntityTypeWork, scriptpkg.EntityTypeProduct:
		return true
	default:
		return false
	}
}

func LimitTranslatedVisualEntities(entities []contract.VisualEntity, limit int) []contract.VisualEntity {
	if limit <= 0 || len(entities) <= limit {
		return entities
	}
	priority := []scriptpkg.EntityType{
		scriptpkg.EntityTypePerson, scriptpkg.EntityTypeLocation, scriptpkg.EntityTypeOrganization,
		scriptpkg.EntityTypeEvent, scriptpkg.EntityTypeWork, scriptpkg.EntityTypeProduct,
	}
	out := make([]contract.VisualEntity, 0, limit)
	used := make([]bool, len(entities))
	for _, kind := range priority {
		for i, entity := range entities {
			if used[i] || entity.Type != kind {
				continue
			}
			out = append(out, entity)
			used[i] = true
			if len(out) == limit {
				return out
			}
		}
	}
	for i, entity := range entities {
		if used[i] {
			continue
		}
		out = append(out, entity)
		if len(out) == limit {
			break
		}
	}
	return out
}

func HasAdditionalEntityCategory(policy mediadomain.MediaExtractionPolicy) bool {
	for _, requested := range policy.Include {
		requested = strings.TrimSpace(requested)
		if strings.EqualFold(requested, mediadomain.ExtractionIncludeEntities) ||
			strings.EqualFold(requested, mediadomain.ExtractionIncludeSpecialNames) {
			continue
		}
		for _, category := range mediadomain.ExtractionIncludeValueCategories {
			if strings.EqualFold(requested, category) {
				return true
			}
		}
	}
	return false
}

// LimitVisualIdentityEntities caps only imageable named identities while
// preserving value annotations that have their own non-image editorial path.
func LimitVisualIdentityEntities(entities []contract.VisualEntity, limit int) []contract.VisualEntity {
	if limit <= 0 {
		return entities
	}
	out := make([]contract.VisualEntity, 0, len(entities))
	identities := 0
	for _, entity := range entities {
		if scriptpkg.IsAnnotationEntityKind(scriptpkg.NormalizeAnnotationType(string(entity.Type))) {
			if identities >= limit {
				continue
			}
			identities++
		}
		out = append(out, entity)
	}
	return out
}

func DeduplicateVisualEntities(entities []contract.VisualEntity) []contract.VisualEntity {
	if len(entities) < 2 {
		return entities
	}
	// A surname-only mention is an alias when exactly one full PERSON name in
	// this segment ends with that surname. VisualNER deliberately preserves
	// mention spans for annotation rebasing, but the image-search surface must
	// not turn "Donald Trump" and "Trump" into two independent identities.
	fullPeopleBySurname := make(map[string]map[string]struct{}, len(entities))
	for _, entity := range entities {
		if scriptpkg.NormalizeAnnotationType(string(entity.Type)) != "PERSON" {
			continue
		}
		name := strings.Fields(NormalizeVisualPersonName(entity.Text))
		if len(name) < 2 {
			continue
		}
		surname := strings.ToLower(name[len(name)-1])
		if fullPeopleBySurname[surname] == nil {
			fullPeopleBySurname[surname] = make(map[string]struct{})
		}
		fullPeopleBySurname[surname][strings.ToLower(strings.Join(name, " "))] = struct{}{}
	}
	out := make([]contract.VisualEntity, 0, len(entities))
	seen := make(map[string]int, len(entities))
	for _, entity := range entities {
		value := strings.TrimSpace(entity.Text)
		if scriptpkg.NormalizeAnnotationType(string(entity.Type)) == "PERSON" {
			value = NormalizeVisualPersonName(value)
			name := strings.Fields(value)
			if len(name) == 1 {
				if owners := fullPeopleBySurname[strings.ToLower(name[0])]; len(owners) == 1 {
					continue
				}
			}
		}
		key := strings.ToLower(strings.Join(strings.Fields(value), " "))
		if key == "" {
			continue
		}
		if existing, exists := seen[key]; exists {
			if out[existing].IdentityID == "" {
				out[existing].IdentityID = entity.IdentityID
				out[existing].CanonicalID = entity.CanonicalID
			}
			if !out[existing].EditorialDecision && entity.EditorialDecision {
				out[existing].EditorialDecision = true
				out[existing].EditorialSurface = entity.EditorialSurface
				out[existing].EditorialType = entity.EditorialType
				out[existing].PromptEcho = entity.PromptEcho
				out[existing].VisualAdmission = entity.VisualAdmission
			}
			for _, intent := range entity.SearchIntents {
				out[existing].SearchIntents = AppendUniqueSemanticIntents(out[existing].SearchIntents, intent)
			}
			continue
		}
		seen[key] = len(out)
		entity.Text = value
		out = append(out, entity)
	}
	return out
}

func FilterOverlayCategoryEntities(entities []contract.VisualEntity) []contract.VisualEntity {
	out := entities[:0]
	for _, entity := range entities {
		switch scriptpkg.NormalizeAnnotationType(string(entity.Type)) {
		case "PERSON", "ORG", "GPE", "PRODUCT", "LOGO", "DATE", "TIME", "CARDINAL", "NUMBER", "ORDINAL", "MONEY", "PERCENT":
			out = append(out, entity)
		}
	}
	return out
}

func AppendUniqueSemanticIntents(existing []string, intent string) []string {
	for _, value := range existing {
		if value == intent {
			return existing
		}
	}
	return append(existing, intent)
}

func NormalizeVisualPersonName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(value, "While "), "while "))
	for _, suffix := range []string{"'s", "’s"} {
		if len(value) > len(suffix) && strings.EqualFold(value[len(value)-len(suffix):], suffix) {
			value = strings.TrimSpace(value[:len(value)-len(suffix)])
			break
		}
	}
	return value
}

// deduplicateVisualEntities collapses grammatical variants of one identity
// before the entity/image fanout. VisualNER may return both a canonical name
// and a sentence surface such as "While Dolly Parton's"; those are one
// person, hence one image query and one overlay.
// NormalizeVisualEntityTypes normalizes the extractor's taxonomy labels while
// preserving the semantic type assigned by BrainRust.
func NormalizeVisualEntityTypes(entities []contract.VisualEntity) {
	for i := range entities {
		entities[i].Type = scriptpkg.EntityType(scriptpkg.NormalizeAnnotationType(string(entities[i].Type)))
	}
}
