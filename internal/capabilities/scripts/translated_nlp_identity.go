// Package scriptgeneration — translated_nlp_identity.go owns the SINGLE
// localization seam between a SOURCE annotation entity and the text of a
// TRANSLATED scene: locating the translated surface a source identity appears
// as, and carrying that identity onto the localized annotation.
//
// Pipeline position:
//
//	SOURCE annotations → matchLocalizedSourceEntities → localized VisualEntity hints
//	                                                 → stampLocalizedSourceIdentity
//	                                                 → localized SceneAnnotations
//
// Why the seam exists: a localized annotation is produced by running the NLP over
// the TRANSLATED text, so its entity surface is whatever the translator wrote
// ("Mike’a Tysona"). The IDENTITY — canonical_entity_id and the identity-scoped
// image binding — belongs to the SOURCE entity and must neither be re-minted
// from that surface nor re-resolved downstream. These functions copy it.
//
// Nothing is ever invented: an entity whose name does not occur in the
// translated scene produces no match, so no mention and no identity is faked.
package scriptgeneration

import (
	"strings"
	"unicode"
	"unicode/utf8"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// localizedSourceMatch is one SOURCE annotation entity grounded in a
// TRANSLATED scene: the identity the source surface owns (type, canonical id,
// image binding) plus the rune span and the display surface of the localized
// text it matched. It is the single matching primitive behind the localized
// annotation projection — the same matches feed both the localized
// VisualEntity hints and the identity inheritance, so the two can never
// disagree about which source entity a localized mention refers to.
type localizedSourceMatch struct {
	Source  scriptpkg.AnnotatedEntity
	Kind    scriptpkg.EntityType
	Span    scriptpkg.AnnotationSpan
	Surface string
}

// matchLocalizedSourceEntities grounds every SOURCE annotation entity in the
// translated scene text and returns the matches, carrying the source identity
// with them. Matching is span-based against the translated text (exact tokens
// first, then the PL/DE inflection forms of a person name); an entity that does
// not occur in the translated scene produces no match, so no identity or
// mention is ever invented.
func matchLocalizedSourceEntities(text, language string, source *scriptpkg.SceneAnnotations) []localizedSourceMatch {
	if source == nil {
		return nil
	}
	all := append(append([]scriptpkg.AnnotatedEntity(nil), source.PrimaryEntities...), source.SecondaryEntities...)
	var out []localizedSourceMatch
	for _, entity := range all {
		kind := localizedSourceEntityType(entity.Type)
		if kind == "" {
			continue
		}
		identity := firstNonEmpty(entity.CanonicalName, entity.Text)
		aliases := []string{identity}
		if kind == scriptpkg.EntityTypePerson {
			identity = normalizeVisualPersonName(identity)
			aliases = []string{identity}
			// Source mention surfaces can include a role or an editorial lead-in
			// ("Trainer Cus D’Amato", "Like Muhammad Ali"). Try complete
			// proper-name suffixes, longest first, against the translated text.
			for _, run := range personNameRuns(identity) {
				for start := 1; start < len(run); start++ {
					aliases = append(aliases, strings.Join(run[start:], " "))
				}
			}
		}
		for _, alias := range aliases {
			span, ok := findExactNameTokenSpan(text, alias)
			if !ok && kind == scriptpkg.EntityTypePerson {
				switch strings.ToLower(language) {
				case "pl", "de":
					span, ok = findInflectedPersonSpan(text, alias, language)
				}
			}
			if !ok || strings.TrimSpace(span.Text) == "" {
				continue
			}
			// The matched span is the authority for the translated surface. The
			// canonical alias remains identity input only; changing the surface
			// back to it would lose grammar carried by the narration (for example
			// German "Mike Tysons") before the timing layer sees it.
			out = append(out, localizedSourceMatch{Source: entity, Kind: kind, Span: span, Surface: span.Text})
			break
		}
	}
	return out
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
		out = append(out, VisualEntity{Text: match.Surface, Type: match.Kind, Score: float32(match.Source.Confidence)})
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
// identity it already had, and an already-stamped id is never overwritten.
func stampLocalizedSourceIdentity(ann *scriptpkg.SceneAnnotations, matches []localizedSourceMatch) {
	if ann == nil || len(matches) == 0 {
		return
	}
	for _, list := range []*[]scriptpkg.AnnotatedEntity{&ann.PrimaryEntities, &ann.SecondaryEntities} {
		for i := range *list {
			entity := &(*list)[i]
			match, ok := localizedMatchForEntity(*entity, matches)
			if !ok {
				continue
			}
			if strings.TrimSpace(entity.CanonicalEntityID) == "" {
				entity.CanonicalEntityID = annotationCanonicalEntityID(match.Source)
			}
			entity.Mentions = prependLocalizedMention(match.Span, entity.Mentions)
			if entity.Image == nil && match.Source.Image != nil {
				binding := *match.Source.Image
				entity.Image = &binding
			}
		}
	}
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

// ── Localized name tokens ─────────────────────────────────────────────
//
// The token layer below is what makes the match survive a localized spelling:
// names are compared token-wise (never as raw offsets into a different string),
// and a PL/DE grammatical suffix on the LAST comparable token is accepted as a
// surface form of the same person. The suffixes are a closed, explicit list
// rather than a stemmer, so an unrelated word that merely shares a prefix is
// still rejected.

type localizedNameToken struct {
	start int
	end   int
	text  string
}

// findExactNameTokenSpan locates a name in the translated text as a token
// sequence (case-insensitive, punctuation-insensitive) and returns the rune span
// of the matched tokens.
func findExactNameTokenSpan(text, candidate string) (scriptpkg.AnnotationSpan, bool) {
	want, have := localizedNameTokens(candidate), localizedNameTokens(text)
	if len(want) == 0 || len(have) < len(want) {
		return scriptpkg.AnnotationSpan{}, false
	}
	for start := 0; start+len(want) <= len(have); start++ {
		matched := true
		for offset := range want {
			if !strings.EqualFold(want[offset].text, have[start+offset].text) {
				matched = false
				break
			}
		}
		if matched {
			first, last := have[start], have[start+len(want)-1]
			return localizedNameSpan(text, first, last), true
		}
	}
	return scriptpkg.AnnotationSpan{}, false
}

// findInflectedPersonSpan locates a multi-token person name whose tokens carry a
// required grammatical suffix (Polish declension, German possessive -s). A
// single-token name is deliberately not inflected: without a second token there
// is no evidence the suffix belongs to a name rather than to a different word.
func findInflectedPersonSpan(text, canonical, language string) (scriptpkg.AnnotationSpan, bool) {
	want, have := localizedNameTokens(canonical), localizedNameTokens(text)
	if len(want) < 2 || len(have) < len(want) {
		return scriptpkg.AnnotationSpan{}, false
	}
	for start := 0; start+len(want) <= len(have); start++ {
		matched := true
		for offset := range want {
			if !localizedInflectedNameTokenMatches(language, want[offset].text, have[start+offset].text) {
				matched = false
				break
			}
		}
		if matched {
			first, last := have[start], have[start+len(want)-1]
			return localizedNameSpan(text, first, last), true
		}
	}
	return scriptpkg.AnnotationSpan{}, false
}

func localizedNameSpan(text string, first, last localizedNameToken) scriptpkg.AnnotationSpan {
	return scriptpkg.AnnotationSpan{
		Text:      text[first.start:last.end],
		StartRune: utf8.RuneCountInString(text[:first.start]),
		EndRune:   utf8.RuneCountInString(text[:last.end]),
	}
}

// localizedInflectedNameTokenMatches reports whether a translated token is an
// accepted inflected surface form of a canonical name token. The canonical token
// is compared whole (case-insensitive) or with one closed-list suffix.
func localizedInflectedNameTokenMatches(language, canonical, surface string) bool {
	canonical, surface = strings.ToLower(canonical), strings.ToLower(surface)
	if canonical == surface {
		return true
	}
	if utf8.RuneCountInString(canonical) < 3 || !strings.HasPrefix(surface, canonical) {
		return false
	}
	suffix := strings.TrimPrefix(surface, canonical)
	switch strings.ToLower(language) {
	case "pl":
		switch suffix {
		case "a", "ie", "y", "i", "u", "owi", "em", "ą", "ę", "om", "ami", "ach", "ów", "’a", "'a":
			return true
		}
	case "de":
		return suffix == "s"
	}
	return false
}

// localizedNameTokens splits text into word tokens (letters/digits, with an
// internal apostrophe kept), preserving the byte offsets so a token span can be
// projected back onto the original rune coordinates.
func localizedNameTokens(text string) []localizedNameToken {
	var out []localizedNameToken
	start := -1
	for offset, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || ((r == '\'' || r == '’') && start >= 0) {
			if start < 0 {
				start = offset
			}
			continue
		}
		if start >= 0 {
			out = append(out, localizedNameToken{start: start, end: offset, text: text[start:offset]})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, localizedNameToken{start: start, end: len(text), text: text[start:]})
	}
	return out
}
