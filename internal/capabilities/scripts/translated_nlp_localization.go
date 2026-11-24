// Package scriptgeneration — translated_nlp_localization.go owns the SINGLE
// localization seam between a SOURCE annotation entity and the text of a
// TRANSLATED scene. Matching and inflection belong to the Rust semantic
// engine; this file only builds the source request and projects the returned
// span onto the localized annotation. The source identity remains the
// authority: id, type and image binding are copied, never re-minted.
package scriptgeneration

import (
	"context"
	"fmt"
	semanticscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/semantic"
	"strings"
	"sync/atomic"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/entitytype"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// localizedSourceMatch is one SOURCE annotation entity grounded in a
// TRANSLATED scene. SourceIndex is the position in the request source list;
// the Rust matcher returns it so projection never has to compare names.
type localizedSourceMatch struct {
	Source      scriptpkg.AnnotatedEntity
	SourceIndex int
	Kind        scriptpkg.EntityType
	Span        scriptpkg.AnnotationSpan
	Surface     string
}

// matchLocalizedSources grounds every SOURCE annotation entity in the
// translated scene through the Rust matcher. Nil matcher fails closed: a
// translated run must never silently fall back to a second Go matcher.
func (r *Runner) matchLocalizedSources(ctx context.Context, text, language string, source *scriptpkg.SceneAnnotations) ([]localizedSourceMatch, error) {
	sources, entities := localizedSourceRequest(source)
	if len(sources) == 0 {
		return nil, nil
	}
	matcher := r.localizedIdentityMatcher()
	if matcher == nil {
		return nil, fmt.Errorf("localized identity matcher is not configured")
	}
	matches, err := matcher.MatchLocalizedIdentities(ctx, language, text, sources)
	if err != nil {
		return nil, err
	}
	return localizedMatchesFromRust(text, entities, matches)
}

// completeLocalizedPersonMatches closes the identity gap for scripts whose
// translation changes the writing system (notably Russian Cyrillic). Rust
// performs the narrow ordered fallback; Go only forwards the translated PERSON
// candidates and projects the returned spans.
func (r *Runner) completeLocalizedPersonMatches(ctx context.Context, text, language string, source *scriptpkg.SceneAnnotations, visual []VisualEntity, existing []localizedSourceMatch) ([]localizedSourceMatch, error) {
	if len(existing) == 0 && len(visual) == 0 {
		return existing, nil
	}
	sources, entities := localizedSourceRequest(source)
	if len(sources) == 0 {
		return existing, nil
	}
	matcher := r.localizedIdentityMatcher()
	if matcher == nil {
		return nil, fmt.Errorf("localized identity matcher is not configured")
	}
	visualTexts := make([]string, 0, len(visual))
	for _, entity := range visual {
		if entity.Type == scriptpkg.EntityTypePerson && strings.TrimSpace(entity.Text) != "" {
			visualTexts = append(visualTexts, entity.Text)
		}
	}
	rustExisting := make([]LocalizedIdentityMatch, 0, len(existing))
	for _, match := range existing {
		rustExisting = append(rustExisting, LocalizedIdentityMatch{
			SourceIndex: match.SourceIndex,
			Kind:        string(match.Kind),
			StartRune:   match.Span.StartRune,
			EndRune:     match.Span.EndRune,
			Surface:     match.Surface,
		})
	}
	matches, err := matcher.CompleteLocalizedIdentities(ctx, language, text, sources, visualTexts, rustExisting)
	if err != nil {
		return nil, err
	}
	return localizedMatchesFromRust(text, entities, matches)
}

// localizedSourceRequest builds the typed source list the Rust matcher sees.
// Entity types are resolved once here; the matcher never receives a raw
// annotation type it would have to reinterpret.
func localizedSourceRequest(source *scriptpkg.SceneAnnotations) ([]LocalizedSourceEntity, []scriptpkg.AnnotatedEntity) {
	if source == nil {
		return nil, nil
	}
	all := append(append([]scriptpkg.AnnotatedEntity(nil), source.PrimaryEntities...), source.SecondaryEntities...)
	sources := make([]LocalizedSourceEntity, 0, len(all))
	entities := make([]scriptpkg.AnnotatedEntity, 0, len(all))
	for _, entity := range all {
		kind := localizedSourceEntityType(entity.Type)
		if kind == "" {
			continue
		}
		sources = append(sources, LocalizedSourceEntity{
			Kind:          string(kind),
			CanonicalName: entity.CanonicalName,
			Text:          entity.Text,
		})
		entities = append(entities, entity)
	}
	return sources, entities
}

// localizedMatchesFromRust validates every returned span against the
// translated text and projects it back onto the source entity. A span that does
// not occur verbatim fails closed.
func localizedMatchesFromRust(text string, entities []scriptpkg.AnnotatedEntity, matches []LocalizedIdentityMatch) ([]localizedSourceMatch, error) {
	runes := []rune(text)
	out := make([]localizedSourceMatch, 0, len(matches))
	for index, match := range matches {
		if match.SourceIndex < 0 || match.SourceIndex >= len(entities) {
			return nil, fmt.Errorf("localized identity match %d has source index %d out of range", index, match.SourceIndex)
		}
		if strings.TrimSpace(match.Kind) == "" || strings.TrimSpace(match.Surface) == "" {
			return nil, fmt.Errorf("localized identity match %d is missing kind or surface", index)
		}
		if match.StartRune < 0 || match.EndRune <= match.StartRune || match.EndRune > len(runes) {
			return nil, fmt.Errorf("localized identity match %d has invalid span %d..%d", index, match.StartRune, match.EndRune)
		}
		if got := string(runes[match.StartRune:match.EndRune]); got != match.Surface {
			return nil, fmt.Errorf("localized identity match %d surface %q does not match text span %q", index, match.Surface, got)
		}
		entity := entities[match.SourceIndex]
		kind := localizedSourceEntityType(entity.Type)
		if !strings.EqualFold(string(kind), match.Kind) {
			return nil, fmt.Errorf("localized identity match %d kind %q does not match source kind %q", index, match.Kind, kind)
		}
		out = append(out, localizedSourceMatch{
			Source:      entity,
			SourceIndex: match.SourceIndex,
			Kind:        kind,
			Span: scriptpkg.AnnotationSpan{
				Text:      match.Surface,
				StartRune: match.StartRune,
				EndRune:   match.EndRune,
			},
			Surface: match.Surface,
		})
	}
	return out, nil
}

// sourceMatchesCoverEntityLimit reports whether the source-grounded matches
// already fill the caller's per-scene entity limit for THIS translation, which
// makes the translated NER call redundant.
//
// The precondition is deliberately narrow. mergeTranslatedNamedEntities drops
// VisualNER's typed named entities (PERSON/ORG/EVENT/WORK/PRODUCT) as soon as
// source matches exist, but it KEEPS VisualNER's remaining entities (locations,
// visual concepts) and appends them BEFORE the matches;
// limitTranslatedVisualEntities then orders PERSON first. So the model
// contribution is guaranteed to fall outside the window ONLY when every match is
// a PERSON — for any other kind, a model location could still take a slot. A
// non-PERSON match therefore refuses the skip rather than silently changing the
// emitted entities.
func sourceMatchesCoverEntityLimit(matches []localizedSourceMatch, limit int) bool {
	if limit <= 0 || len(matches) < limit {
		return false
	}
	for _, match := range matches {
		if match.Kind != scriptpkg.EntityTypePerson {
			return false
		}
	}
	return true
}

// localizedSourceVisualEntities projects the matches onto the localized
// VisualEntity hints the translated-NER merge consumes.
func localizedSourceVisualEntities(matches []localizedSourceMatch) []VisualEntity {
	if len(matches) == 0 {
		return nil
	}
	out := make([]VisualEntity, 0, len(matches))
	for _, match := range matches {
		out = append(out, VisualEntity{
			Text: match.Surface, Type: match.Kind, Score: float32(match.Source.Confidence), Role: match.Source.Role,
			EditorialDecision: match.Source.EditorialDecision, EditorialSurface: match.Source.EditorialSurface,
			EditorialType: match.Source.EditorialType, PromptEcho: match.Source.PromptEcho,
			VisualAdmission: match.Source.VisualAdmission,
		})
	}
	return out
}

// stampLocalizedSourceIdentity copies the SOURCE identity onto every localized
// annotation entity that grounds to it: the canonical_entity_id (so a
// translated document joins the entity media index under the SAME identity as
// the source, instead of re-deriving "person:tyson" from "Mike'a Tysona") and,
// when the source carries one, the identity-scoped image binding — an image of
// a person does not depend on the language the card is displayed in.
//
// Nothing is invented: an entity with no matching source keeps whatever
// identity it already had, and an already-stamped id is never overwritten. A
// matched source with no derivable canonical id fails closed rather than
// letting the localized surface re-mint one.
func stampLocalizedSourceIdentity(ann *scriptpkg.SceneAnnotations, matches []localizedSourceMatch) error {
	if ann == nil || len(matches) == 0 {
		return nil
	}
	for _, list := range []*[]scriptpkg.AnnotatedEntity{&ann.PrimaryEntities, &ann.SecondaryEntities} {
		for i := range *list {
			entity := &(*list)[i]
			match, ok := localizedMatchForEntity(*entity, matches)
			if !ok {
				continue
			}
			if strings.TrimSpace(entity.CanonicalEntityID) == "" {
				id := annotationCanonicalEntityID(match.Source)
				if strings.TrimSpace(id) == "" {
					return fmt.Errorf("localized identity: source entity %q has no canonical id", match.Surface)
				}
				entity.CanonicalEntityID = id
			}
			entity.Mentions = prependLocalizedMention(match.Span, entity.Mentions)
			if !entity.EditorialDecision && match.Source.EditorialDecision {
				entity.EditorialDecision = true
				entity.EditorialSurface = match.Source.EditorialSurface
				entity.EditorialType = match.Source.EditorialType
				entity.PromptEcho = match.Source.PromptEcho
				entity.VisualAdmission = match.Source.VisualAdmission
			}
			if entity.Image == nil && match.Source.Image != nil {
				binding := *match.Source.Image
				entity.Image = &binding
			}
			// Geocoding belongs to the source place identity. Preserve its
			// validated point on the localized surface so map planning can use
			// the localized spoken mention without issuing a second lookup.
			if entity.Geo == nil && match.Source.Geo != nil {
				coordinate := *match.Source.Geo
				entity.Geo = &coordinate
			}
		}
	}
	return nil
}

// prependLocalizedMention makes the source-grounded translated span the
// authority for downstream spoken timing while retaining any additional
// mentions VisualNER found in the same localized text.
func prependLocalizedMention(match scriptpkg.AnnotationSpan, mentions []scriptpkg.AnnotationSpan) []scriptpkg.AnnotationSpan {
	out := make([]scriptpkg.AnnotationSpan, 0, len(mentions)+1)
	out = append(out, match)
	for _, mention := range mentions {
		if mention.StartRune == match.StartRune && mention.EndRune == match.EndRune {
			continue
		}
		out = append(out, mention)
	}
	return out
}

// localizedMatchForEntity finds the source match a localized annotation entity
// grounds to: the same normalized kind plus a rune-span overlap with one of the
// entity's grounded mentions, falling back to an exact display-surface equality
// for an entity the projection emitted without a mention span. Kind and span are
// both required, so a distinct entity can never inherit another's identity.
func localizedMatchForEntity(entity scriptpkg.AnnotatedEntity, matches []localizedSourceMatch) (localizedSourceMatch, bool) {
	kind := normalizeEntityAnnotationType(entity.Type)
	if kind == "" {
		return localizedSourceMatch{}, false
	}
	for _, match := range matches {
		if normalizeEntityAnnotationType(string(match.Kind)) != kind {
			continue
		}
		if annotationSpansOverlap(entity.Mentions, match.Span) {
			return match, true
		}
	}
	for _, match := range matches {
		if normalizeEntityAnnotationType(string(match.Kind)) != kind {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(entity.CanonicalName), strings.TrimSpace(match.Surface)) {
			return match, true
		}
	}
	return localizedSourceMatch{}, false
}

// annotationSpansOverlap reports whether any grounded mention of an annotation
// entity overlaps the matched source span. Both spans are rune offsets into the
// SAME translated text.
func annotationSpansOverlap(mentions []scriptpkg.AnnotationSpan, span scriptpkg.AnnotationSpan) bool {
	for _, mention := range mentions {
		if mention.StartRune < span.EndRune && span.StartRune < mention.EndRune {
			return true
		}
	}
	return false
}

func localizedSourceEntityType(raw string) scriptpkg.EntityType {
	switch entitytype.Fold(raw) {
	case entitytype.Person:
		return scriptpkg.EntityTypePerson
	case entitytype.Place:
		return scriptpkg.EntityTypeLocation
	case entitytype.Org:
		return scriptpkg.EntityTypeOrganization
	case entitytype.Logo:
		return scriptpkg.EntityType("BRAND")
	case entitytype.Event:
		return scriptpkg.EntityTypeEvent
	case entitytype.WorkOfArt:
		return scriptpkg.EntityTypeWork
	case entitytype.Product:
		return scriptpkg.EntityTypeProduct
	default:
		return ""
	}
}

// LocalizedSourceEntity is one SOURCE annotation identity offered to the Rust
// localized-identity matcher. Kind is already normalized to the annotation
// vocabulary (PERSON, LOCATION, ORGANIZATION, ...).
type LocalizedSourceEntity = semanticscript.LocalizedSourceEntity

// LocalizedIdentityMatch is one SOURCE identity grounded in translated text.
// The span is in rune coordinates and Surface is the verbatim translated
// surface; the canonical id remains on the source identity, never here.
type LocalizedIdentityMatch = semanticscript.LocalizedIdentityMatch

// LocalizedIdentityMatcher is the Rust boundary for the localization seam.
// Match is called before translated NER; Complete is called after it with the
// translated PERSON candidates and may only fill the still-unmatched people.
type LocalizedIdentityMatcher = semanticscript.LocalizedIdentityMatcher

// defaultLocalizedIdentityMatcher is a test-only hook. Production leaves it
// unset and fails closed; the package TestMain installs the former Go matcher
// as an oracle for the translated-NLP integration tests.
//
// The hook is an atomic.Value of a one-field wrapper because atomic.Value
// cannot store a nil interface: an unset hook reads as nil, never as a typed
// nil, and a TestMain swap cannot race a reader.
var defaultLocalizedIdentityMatcher atomic.Value // localizedMatcherHook

type localizedMatcherHook struct{ matcher LocalizedIdentityMatcher }

func (r *Runner) localizedIdentityMatcher() LocalizedIdentityMatcher {
	if r != nil && r.localizedIdentity != nil {
		return r.localizedIdentity
	}
	if v := defaultLocalizedIdentityMatcher.Load(); v != nil {
		return v.(localizedMatcherHook).matcher
	}
	return nil
}
