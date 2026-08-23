package imagesearch

import (
	"context"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Resolver is the deterministic Image Search Intent resolver. It is
// stateless and safe for concurrent use; the only I/O is the injected
// extractor (the conservative CPU extractor in tests, the hybrid extractor
// in production). Language ("en" default, "it") selects the knowledge-base
// surfaces, the structural vocabulary (negation, pronouns, subordinate
// markers, fight context) and the MONEY/DATE/EVENT patterns; the canonical
// identities and queries stay language-invariant, so Italian input
// canonicalizes to the same visual intent the English battery certifies.
type Resolver struct {
	extractor EntityExtractor
}

// NewResolver builds the resolver over the given extractor. The extractor
// provides the BASELINE person/place surface; the resolver's knowledge base
// is authoritative for the typed, disambiguated identities the battery
// certifies.
func NewResolver(extractor EntityExtractor) *Resolver {
	return &Resolver{extractor: extractor}
}

// Resolve turns one scene sentence into its image search decision. It never
// fails: extraction problems degrade to an empty baseline, and the knowledge
// base + structural patterns still run.
func (r *Resolver) Resolve(ctx context.Context, req Request) ImageSearchDecision {
	text := strings.TrimSpace(req.Text)
	dec := ImageSearchDecision{}
	if text == "" {
		dec.NoImageReason = "empty_text"
		return dec
	}
	lang := normalizeLanguage(req.Language)
	lower := strings.ToLower(text)

	// 1. Baseline extraction (conservative CPU surface).
	baseline := r.extractBaseline(ctx, req)

	// 2. Knowledge-base identities (typed + disambiguated).
	entities, contexts := resolveKnown(text, lang)

	// 3. Baseline entities not already covered by the knowledge base.
	entities = appendBaselineEntities(entities, baseline, text, lang)

	// 4. Value entities → the visual system (MONEY / DATE / EVENT).
	dec.Visual, dec.ImportantPhrases = resolveValues(text, baseline, lang)

	// 5. Negation: "Tyson Fury, not Mike Tyson" excludes Mike Tyson.
	entities, dec.Negated = applyNegation(text, entities, lang)

	// 6. Coreference: a leading pronoun (or a pro-drop Italian subject)
	// grounds on the prior sentence.
	entities = r.applyCoreference(text, req, entities, lang)

	// 7. The no-image gate.
	dec.Entities = orderEntities(entities)
	dec.Contexts = contexts
	if len(dec.Entities) == 0 {
		dec.Required = false
		dec.NoImageReason = "no_visual_entity"
		if startsWithPronoun(text, lang) {
			dec.NoImageReason = "pronoun_without_antecedent"
		}
		return dec
	}

	// 8. Primary + ordered queries.
	dec.Required = true
	dec.Primary = primaryEntity(dec.Entities, text, lower, lang)
	dec.Queries = buildQueries(dec, text, lower, lang)
	return dec
}

// normalizeLanguage maps the request language onto the two certified modes.
// Unknown / empty languages fall back to English (the battery's SSOT).
func normalizeLanguage(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "it", "it-it", "it-it-001", "italian", "italiano":
		return "it"
	default:
		return "en"
	}
}

// extractBaseline runs the injected extractor and returns its result, or nil
// when the extractor is unavailable or failed (the decision still proceeds on
// the knowledge base alone).
func (r *Resolver) extractBaseline(ctx context.Context, req Request) *scriptpkg.EntityResult {
	if r.extractor == nil {
		return nil
	}
	result, err := r.extractor.ExtractEntities(ctx, scriptpkg.EntityExtractionRequest{
		Text: req.Text, Language: req.Language, EntityCount: 10,
	})
	if err != nil || result == nil {
		return nil
	}
	return result
}

// ── Knowledge-base pass ───────────────────────────────────────────────

// resolveKnown matches the knowledge base (for the request language) and
// turns the hits into typed entities. A product whose brand co-occurs
// ("Vision Pro" + "Apple") is emitted as the merged branded product ("Apple
// Vision Pro") and its brand is marked as folded (represented by the product
// query). Disambiguated persons additionally produce a CONTEXT annotation
// ("basketball", "actor").
func resolveKnown(text string, lang string) ([]ResolvedEntity, []ResolvedEntity) {
	var entities []ResolvedEntity
	var contexts []ResolvedEntity
	brandPresent := make(map[string]bool)
	for _, hit := range matchKnownEntities(text, lang) {
		entry := hit.entry
		if entry.Brand != "" {
			brandPresent[entry.Brand] = brandPresent[entry.Brand] || strings.Contains(text, entry.Brand)
		}
	}
	for _, hit := range matchKnownEntities(text, lang) {
		entry := hit.entry
		surface := entry.Name
		if !entry.Generic {
			// Italian input that matched an entry's SurfacesIT canonicalizes
			// to the English Name ("Torre Eiffel" → "Eiffel Tower",
			// "Arabia Saudita" → "Saudi Arabia"), so entities and queries
			// are language-invariant. Entries without SurfacesIT keep the
			// verbatim surface ("Michael B. Jordan" keeps its period).
			if lang == "it" && len(entry.SurfacesIT) > 0 {
				surface = entry.Name
			} else {
				surface = hit.surface
			}
		}
		entity := ResolvedEntity{
			Type:        entry.Type,
			Text:        surface,
			Verbatim:    hit.surface,
			QueryName:   entry.QueryName,
			Hint:        entry.Hint,
			Domain:      entry.Domain,
			KindWord:    entry.KindWord,
			MentionAt:   runeOffset(text, hit.surface),
			CanonicalID: capabilityentities.CanonicalEntityID(entry.Type, entry.Name),
		}
		if entry.Brand != "" && brandPresent[entry.Brand] {
			entity.Text = entry.Name // merged: "Apple Vision Pro"
			entities = append(entities, entity)
			continue
		}
		if entry.Disambiguates {
			contexts = append(contexts, ResolvedEntity{Type: "CONTEXT", Text: entry.Hint, MentionAt: entity.MentionAt})
		}
		entities = append(entities, entity)
	}
	// Mark brand orgs folded when their product merged ("Apple" carries no
	// query of its own; the product query represents it).
	products := make(map[string]bool)
	for _, e := range entities {
		if e.Type == "PRODUCT" {
			products[e.Text] = true
		}
	}
	for i := range entities {
		if entities[i].Type != "ORG" {
			continue
		}
		for product := range products {
			if strings.HasPrefix(product, entities[i].Text+" ") {
				entities[i].FoldedIntoProduct = true
				break
			}
		}
	}
	return entities, contexts
}

// appendBaselineEntities adds the extractor's persons/places that the
// knowledge base did not already cover. Dedup is VALUE-only (any type): the
// KB is authoritative for known identities, so the conservative extractor's
// weaker typing of the same surface (e.g. "Saudi Arabia" as PERSON, "Vision
// Pro" as PERSON) must never surface as a second, conflicting entity. KEYWORD
// concepts are never entities. The dedup set is seeded with the KB surfaces
// for the request language ("Torre Eiffel", "Arabia Saudita", "Filippine"),
// so the extractor's Italian spans collapse onto their canonical identity.
func appendBaselineEntities(entities []ResolvedEntity, baseline *scriptpkg.EntityResult, text string, lang string) []ResolvedEntity {
	if baseline == nil {
		return entities
	}
	// The dedup set covers the resolved entities AND every knowledge-base
	// surface, so the extractor's weaker spellings ("Vision Pro" as PERSON,
	// "Saudi Arabia" as PERSON, "Tesla's Cybertruck" as PERSON) collapse onto
	// the KB identity instead of surfacing as conflicting entities.
	seen := make(map[string]bool, len(entities))
	for _, e := range entities {
		seen[strings.ToLower(strings.TrimSpace(e.Text))] = true
	}
	for _, entry := range knownEntities {
		seen[strings.ToLower(strings.TrimSpace(entry.Name))] = true
		for _, surface := range surfacesFor(entry, lang) {
			seen[strings.ToLower(strings.TrimSpace(surface))] = true
		}
	}
	add := func(value, typ string) {
		value = normalizeBaselineValue(value)
		if value == "" || strings.EqualFold(typ, "KEYWORD") {
			return
		}
		if seen[strings.ToLower(value)] {
			return
		}
		// The extractor never emits single-word names by itself (only known
		// places); after connector trimming, a single leftover capitalized
		// word ("Success of" → "Success") is a sentence fragment, not a
		// person. Acronyms ("NASA") are the exception.
		if isSingleWord(value) && !isAcronym(value) {
			return
		}
		seen[strings.ToLower(value)] = true
		entities = append(entities, ResolvedEntity{
			Type:        typ,
			Text:        value,
			MentionAt:   runeOffset(text, value),
			CanonicalID: capabilityentities.CanonicalEntityID(typ, value),
		})
	}
	for _, e := range baseline.Persons {
		add(e.Value, "PERSON")
	}
	for _, e := range baseline.Places {
		typ := e.Type
		if typ == "" {
			typ = "GPE"
		}
		add(e.Value, typ)
	}
	for _, e := range baseline.Concepts {
		if strings.EqualFold(strings.TrimSpace(e.Type), "KEYWORD") {
			continue
		}
		add(e.Value, e.Type)
	}
	return entities
}

// canonicalValue is the dedup key of an entity: lowercase type + normalized
// name.
func canonicalValue(typ, name string) string {
	return strings.ToLower(strings.TrimSpace(typ)) + "\x00" + strings.ToLower(strings.TrimSpace(name))
}

// normalizeBaselineValue cleans the extractor's span quirks before dedup:
// leading contrast openers ("Unlike Mike Tyson" → "Mike Tyson"), trailing
// connectors ("Manny Pacquiao and" → "Manny Pacquiao", "Manny Pacquiao e"
// → "Manny Pacquiao") and possessives ("Tesla's Cybertruck" → "Tesla
// Cybertruck").
func normalizeBaselineValue(value string) string {
	value = strings.TrimSpace(value)
	fields := strings.Fields(value)
	// Strip a sentence-initial contrast opener the extractor folded into the
	// span ("Unlike Mike Tyson" → "Mike Tyson", deduped onto the KB id).
	for len(fields) > 0 {
		first := strings.ToLower(strings.Trim(fields[0], ".,;:!?\"'"))
		if !leadingContrastWords[first] {
			break
		}
		fields = fields[1:]
	}
	for len(fields) > 0 {
		last := strings.ToLower(strings.Trim(fields[len(fields)-1], ".,;:!?\"'"))
		switch last {
		// English connectors.
		case "and", "or", "of", "the", "for", "&", "in", "on", "at", "to", "with", "from",
			// Italian connectors.
			"e", "ed", "è", "di", "il", "lo", "la", "i", "gli", "le", "per", "da", "con",
			"a", "su", "che", "ha", "nel", "nella", "nei", "nelle", "del", "della", "dei", "degli":
			fields = fields[:len(fields)-1]
			continue
		}
		break
	}
	value = strings.ReplaceAll(strings.Join(fields, " "), "'s", "")
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

// isSingleWord reports whether value contains exactly one whitespace token.
func isSingleWord(value string) bool {
	return len(strings.Fields(value)) == 1
}

// isAcronym reports whether value looks like an all-uppercase acronym.
func isAcronym(value string) bool {
	if len(value) < 2 || len(value) > 8 {
		return false
	}
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// ── Value pass (visual system, not image search) ─────────────────────

// resolveValues detects MONEY / DATE / EVENT surfaces (for the request
// language). These go to the Visual list (animated money graphics, date
// badges, event badges) and never become image search queries. Money phrases
// ("earned more than $100 million", "guadagnato più di 100 milioni di
// dollari") are surfaced as ImportantPhrases for the visual scheduler.
func resolveValues(text string, baseline *scriptpkg.EntityResult, lang string) ([]ResolvedEntity, []string) {
	var visual []ResolvedEntity
	seen := map[string]bool{}
	var phrases []string

	addVisual := func(typ, value string) {
		key := canonicalValue(typ, value)
		if seen[key] {
			return
		}
		seen[key] = true
		visual = append(visual, ResolvedEntity{
			Type:        typ,
			Text:        value,
			MentionAt:   runeOffset(text, value),
			CanonicalID: capabilityentities.CanonicalEntityID(typ, value),
		})
	}

	for _, pattern := range moneyPatternsFor(lang) {
		if m := pattern.value.FindString(text); m != "" {
			addVisual("MONEY", m)
		}
		if p := pattern.phrase.FindString(text); p != "" {
			phrases = append(phrases, p)
		}
	}
	decade, event := decadeRE, eventRE
	if lang == "it" {
		decade, event = decadeREIT, eventREIT
	}
	if m := decade.FindString(text); m != "" {
		addVisual("DATE", m)
	}
	if m := event.FindString(text); m != "" {
		addVisual("EVENT", m)
	}

	// Baseline important phrases enrich the editorial surface, deduped.
	if baseline != nil {
		for _, phrase := range baseline.ImportantPhrases {
			if phrase = strings.TrimSpace(phrase); phrase != "" {
				phrases = append(phrases, phrase)
			}
		}
	}
	phrases = uniqueStrings(phrases)
	return visual, phrases
}

// ── Negation pass ─────────────────────────────────────────────────────

// applyNegation moves an entity to the Negated list when a negation
// construction ("not Mike Tyson", "instead of Mike Tyson", "rather than
// Mike Tyson", "unlike Mike Tyson", "non/invece di/piuttosto che Mike
// Tyson") precedes it. A phrase negates only the FIRST entity mentioned
// after it (within the three-token window), so in "Unlike Mike Tyson, Floyd
// Mayweather …" only Mike Tyson is excluded — the phrase can never reach
// across another entity. Negated entities never drive a query.

// buildQueries produces the ordered image search query list: the primary
// query (with its visual context folded in), one query per remaining
// imageable entity, and the optional "N1 N2 fight" event query when two known
// boxers co-occur in a real fight context.
func buildQueries(dec ImageSearchDecision, text, lower string, lang string) []string {
	entities := dec.Entities
	if len(entities) == 0 || dec.Primary == nil {
		return nil
	}
	pair := boxingPair(entities)
	queries := []string{primaryQuery(dec, text, lower, pair, lang)}

	primaryKey := canonicalValue(dec.Primary.Type, dec.Primary.Text)
	for _, e := range entities {
		if canonicalValue(e.Type, e.Text) == primaryKey {
			continue
		}
		if e.FoldedIntoProduct {
			continue
		}
		// A place that was already folded into the primary query ("Eiffel
		// Tower Paris") never repeats as a standalone query.
		if (e.Type == "GPE" || e.Type == "LOCATION") && strings.Contains(queries[0], e.Text) {
			continue
		}
		if q := entityQuery(e, pair, text); q != "" {
			queries = append(queries, q)
		}
	}
	if pair && hasFightContext(lower, lang) {
		boxers := boxingPersons(entities)
		queries = append(queries, boxers[0].querySurface()+" "+boxers[1].querySurface()+" fight")
	}
	return uniqueStrings(queries)
}

// boxingPair reports whether at least two non-negated known boxers co-occur.
// A pair switches person queries to plain names (the fight owns the context).
func boxingPair(entities []ResolvedEntity) bool {
	return len(boxingPersons(entities)) >= 2
}

func boxingPersons(entities []ResolvedEntity) []ResolvedEntity {
	var boxers []ResolvedEntity
	for _, e := range entities {
		if e.Type == "PERSON" && e.Domain == "boxing" {
			boxers = append(boxers, e)
		}
	}
	return boxers
}

// primaryQuery builds the primary query with its visual context folded in:
//
//	person:           "Floyd Mayweather" | "Mike Tyson boxer" | "Michael Jordan basketball"
//	product:          "Apple Vision Pro" | "Tesla Cybertruck" | "SpaceX Starship"
//	landmark/location:"Eiffel Tower Paris" (place folded)
//	org:              "Apple company" | "Jaguar car" | "Mayweather Promotions"
//	animal:           "jaguar animal Amazon rainforest" (kind + place folded)
//	object:           "red apple fruit" (adjective + kind folded)
//	category:         "real estate"
//
// Queries are built from the canonical Name/QueryName, so the same visual
// intent is certified in every language.
func primaryQuery(dec ImageSearchDecision, text, lower string, pair bool, lang string) string {
	p := dec.Primary
	if p == nil {
		return ""
	}
	switch p.Type {
	case "PERSON":
		return personQuery(*p, pair)
	case "PRODUCT":
		return p.querySurface()
	case "LANDMARK", "LOCATION":
		if place := coOccurringPlace(dec.Entities, p); place != "" {
			return p.querySurface() + " " + place
		}
		return p.querySurface()
	case "ORG":
		return orgQuery(*p)
	case "ANIMAL":
		q := p.querySurface()
		if p.KindWord != "" {
			q += " " + p.KindWord
		}
		if place := coOccurringPlace(dec.Entities, p); place != "" {
			q += " " + place
		}
		return q
	case "OBJECT":
		q := objectQuery(*p, text, lang)
		// A qualified object carries its retrieval hint ("Mercury planet");
		// plain objects ("red apple fruit") have none.
		if p.Hint != "" {
			q += " " + p.Hint
		}
		return q
	default:
		return p.querySurface()
	}
}

// entityQuery is the secondary-entity query. Persons follow the pair rule;
// orgs use their hint; everything else uses its surface.
func entityQuery(e ResolvedEntity, pair bool, text string) string {
	switch e.Type {
	case "PERSON":
		return personQuery(e, pair)
	case "ORG":
		return orgQuery(e)
	case "LANDMARK", "LOCATION", "GPE", "PRODUCT", "ANIMAL", "OBJECT", "CATEGORY":
		return e.querySurface()
	default:
		return ""
	}
}
