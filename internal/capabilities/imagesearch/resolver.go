package imagesearch

import (
	"context"
	"sort"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// Resolver is the deterministic Image Search Intent resolver. It is
// stateless and safe for concurrent use; the only I/O is the injected
// extractor (the conservative CPU extractor in tests, the hybrid extractor
// in production).
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
	lower := strings.ToLower(text)

	// 1. Baseline extraction (conservative CPU surface).
	baseline := r.extractBaseline(ctx, req)

	// 2. Knowledge-base identities (typed + disambiguated).
	entities, contexts := resolveKnown(text)

	// 3. Baseline entities not already covered by the knowledge base.
	entities = appendBaselineEntities(entities, baseline, text)

	// 4. Value entities → the visual system (MONEY / DATE / EVENT).
	dec.Visual, dec.ImportantPhrases = resolveValues(text, baseline)

	// 5. Negation: "Tyson Fury, not Mike Tyson" excludes Mike Tyson.
	entities, dec.Negated = applyNegation(text, entities)

	// 6. Coreference: a leading pronoun grounds on the prior sentence.
	entities = r.applyCoreference(text, req, entities)

	// 7. The no-image gate.
	dec.Entities = orderEntities(entities)
	dec.Contexts = contexts
	if len(dec.Entities) == 0 {
		dec.Required = false
		dec.NoImageReason = "no_visual_entity"
		if startsWithPronoun(text) {
			dec.NoImageReason = "pronoun_without_antecedent"
		}
		return dec
	}

	// 8. Primary + ordered queries.
	dec.Required = true
	dec.Primary = primaryEntity(dec.Entities, text, lower)
	dec.Queries = buildQueries(dec, text, lower)
	return dec
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

// resolveKnown matches the knowledge base and turns the hits into typed
// entities. A product whose brand co-occurs ("Vision Pro" + "Apple") is
// emitted as the merged branded product ("Apple Vision Pro") and its brand is
// marked as folded (represented by the product query). Disambiguated persons
// additionally produce a CONTEXT annotation ("basketball", "actor").
func resolveKnown(text string) ([]ResolvedEntity, []ResolvedEntity) {
	var entities []ResolvedEntity
	var contexts []ResolvedEntity
	brandPresent := make(map[string]bool)
	for _, hit := range matchKnownEntities(text) {
		entry := hit.entry
		if entry.Brand != "" {
			brandPresent[entry.Brand] = brandPresent[entry.Brand] || strings.Contains(text, entry.Brand)
		}
	}
	for _, hit := range matchKnownEntities(text) {
		entry := hit.entry
		surface := entry.Name
		if !entry.Generic {
			surface = hit.surface
		}
		entity := ResolvedEntity{
			Type:        entry.Type,
			Text:        surface,
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
// concepts are never entities.
func appendBaselineEntities(entities []ResolvedEntity, baseline *scriptpkg.EntityResult, text string) []ResolvedEntity {
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
		for _, surface := range entry.Surfaces {
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
// trailing connectors ("Manny Pacquiao and" → "Manny Pacquiao") and
// possessives ("Tesla's Cybertruck" → "Tesla Cybertruck").
func normalizeBaselineValue(value string) string {
	value = strings.TrimSpace(value)
	fields := strings.Fields(value)
	for len(fields) > 0 {
		last := strings.ToLower(strings.Trim(fields[len(fields)-1], ".,;:!?\"'"))
		switch last {
		case "and", "or", "of", "the", "for", "&", "in", "on", "at", "to", "with", "from":
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

// resolveValues detects MONEY / DATE / EVENT surfaces. These go to the Visual
// list (animated money graphics, date badges, event badges) and never become
// image search queries. Money phrases ("earned more than $100 million") are
// surfaced as ImportantPhrases for the visual scheduler.
func resolveValues(text string, baseline *scriptpkg.EntityResult) ([]ResolvedEntity, []string) {
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

	for _, pattern := range moneyPatterns {
		if m := pattern.value.FindString(text); m != "" {
			addVisual("MONEY", m)
		}
		if p := pattern.phrase.FindString(text); p != "" {
			phrases = append(phrases, p)
		}
	}
	if m := decadeRE.FindString(text); m != "" {
		addVisual("DATE", m)
	}
	if m := eventRE.FindString(text); m != "" {
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

// applyNegation moves an entity to the Negated list when a negation particle
// (not/no/never/neither/nor) occurs within the three tokens before its first
// mention. The negated entity never drives a query.
func applyNegation(text string, entities []ResolvedEntity) ([]ResolvedEntity, []ResolvedEntity) {
	tokens := tokenizeWords(text)
	var kept, negated []ResolvedEntity
	for _, entity := range entities {
		if entityNegated(tokens, entity.Text) {
			negated = append(negated, entity)
			continue
		}
		kept = append(kept, entity)
	}
	return kept, negated
}

// entityNegated checks the window before the entity's first verbatim mention:
// the entity's words must occur as a consecutive token run, and a negation
// particle must sit within the three tokens before that run.
func entityNegated(tokens []string, entityText string) bool {
	words := strings.Fields(entityText)
	if len(words) == 0 {
		return false
	}
	trim := func(tok string) string { return strings.Trim(tok, ".,;:!?\"'()") }
	for i := 0; i+len(words) <= len(tokens); i++ {
		match := true
		for j, word := range words {
			if !strings.EqualFold(trim(tokens[i+j]), word) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		for k := i - 1; k >= 0 && k >= i-3; k-- {
			if negationParticles[strings.ToLower(trim(tokens[k]))] {
				return true
			}
		}
		return false
	}
	return false
}

// ── Coreference pass ──────────────────────────────────────────────────

// applyCoreference grounds a leading pronoun on the most recent prior
// person: "He later invested …" or (after a subordinate clause) "…, he
// expanded Mayweather Promotions …". Without a supplied antecedent the
// pronoun is never resolved ("His fortune changed …" → no entity).
func (r *Resolver) applyCoreference(text string, req Request, entities []ResolvedEntity) []ResolvedEntity {
	hasPronoun := startsWithPronoun(text) || (startsSubordinateClause(text) && sentenceHasPronoun(text))
	if !hasPronoun || len(req.PriorPersons) == 0 {
		return entities
	}
	prior := req.PriorPersons[0]
	if strings.TrimSpace(prior) == "" {
		return entities
	}
	entities = append(entities, ResolvedEntity{
		Type:        "PERSON",
		Text:        prior,
		MentionAt:   runeOffset(text, leadingToken(text)),
		CanonicalID: capabilityentities.CanonicalEntityID("PERSON", prior),
	})
	return entities
}

// sentenceHasPronoun reports whether the sentence contains a resolvable
// pronoun anywhere ("…, he expanded …").
func sentenceHasPronoun(text string) bool {
	for _, token := range tokenizeWords(text) {
		if pronouns[strings.ToLower(strings.Trim(token, ".,;:!?\"'"))] {
			return true
		}
	}
	return false
}

// ── Ordering, primary and queries ─────────────────────────────────────

// entityPriority is the editorial priority for primary selection. PERSON
// wins, then the merged product, then generic subjects, then locations,
// orgs, places and categories.
func entityPriority(typ string) int {
	switch typ {
	case "PERSON":
		return 100
	case "PRODUCT":
		return 95
	case "ANIMAL", "OBJECT":
		return 92
	case "LANDMARK", "LOCATION":
		return 90
	case "ORG":
		return 80
	case "GPE":
		return 70
	case "CATEGORY":
		return 60
	default:
		return 0
	}
}

// orderEntities sorts by editorial priority (desc) then first mention (asc),
// deterministically.
func orderEntities(entities []ResolvedEntity) []ResolvedEntity {
	out := append([]ResolvedEntity(nil), entities...)
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := entityPriority(out[i].Type), entityPriority(out[j].Type)
		if pi != pj {
			return pi > pj
		}
		return out[i].MentionAt < out[j].MentionAt
	})
	return out
}

// primaryEntity picks the entity that must drive the primary image. For
// persons, a coreference-resolved subject wins; otherwise a leading
// subordinate clause ("After … , Floyd …") makes the LAST-mentioned person
// the main-clause subject, else the first-mentioned person wins.
func primaryEntity(entities []ResolvedEntity, text, lower string) *ResolvedEntity {
	if len(entities) == 0 {
		return nil
	}
	persons := make([]ResolvedEntity, 0, len(entities))
	for _, e := range entities {
		if e.Type == "PERSON" {
			persons = append(persons, e)
		}
	}
	if len(persons) > 0 {
		if startsWithPronoun(text) {
			return &persons[0] // the resolved subject
		}
		// A person whose text is not in the sentence is a coreference
		// resolution — it is the subject.
		for _, p := range persons {
			if !strings.Contains(lower, strings.ToLower(p.Text)) {
				return &persons[0]
			}
		}
		if startsSubordinateClause(text) {
			return &persons[len(persons)-1]
		}
		return &persons[0]
	}
	return &entities[0]
}

// buildQueries produces the ordered image search query list: the primary
// query (with its visual context folded in), one query per remaining
// imageable entity, and the optional "N1 N2 fight" event query when two known
// boxers co-occur in a real fight context.
func buildQueries(dec ImageSearchDecision, text, lower string) []string {
	entities := dec.Entities
	if len(entities) == 0 || dec.Primary == nil {
		return nil
	}
	pair := boxingPair(entities)
	queries := []string{primaryQuery(dec, text, lower, pair)}

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
	if pair && hasFightContext(lower) {
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
func primaryQuery(dec ImageSearchDecision, text, lower string, pair bool) string {
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
		return objectQuery(*p, text)
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

// personQuery applies the hint only to a single known person; two co-occurring
// boxers use plain names.
func personQuery(e ResolvedEntity, pair bool) string {
	if pair {
		return e.querySurface()
	}
	if e.Hint != "" {
		return e.querySurface() + " " + e.Hint
	}
	return e.querySurface()
}

// orgQuery applies the retrieval hint ("Apple company", "Jaguar car").
func orgQuery(e ResolvedEntity) string {
	if e.Hint != "" {
		return e.querySurface() + " " + e.Hint
	}
	return e.querySurface()
}

// objectQuery folds the immediately-preceding adjective ("red") into the
// query. The kind word is already part of the canonical name ("apple
// fruit"), so the result is "red apple fruit".
func objectQuery(e ResolvedEntity, text string) string {
	q := e.querySurface()
	if q == "" {
		return ""
	}
	if adj := precedingAdjective(text, e.Text); adj != "" {
		q = adj + " " + q
	}
	return q
}

// coOccurringPlace returns the GPE/LOCATION that visually grounds a landmark
// or animal query ("Paris" for "Eiffel Tower Paris").
func coOccurringPlace(entities []ResolvedEntity, primary *ResolvedEntity) string {
	for _, e := range entities {
		if canonicalValue(e.Type, e.Text) == canonicalValue(primary.Type, primary.Text) {
			continue
		}
		if e.Type == "GPE" || e.Type == "LOCATION" {
			return e.Text
		}
	}
	return ""
}

// precedingAdjective returns the first non-article token immediately before
// the object's verbatim mention ("a red apple" → "red"). The surface is a
// multi-word concept ("apple fruit"); only its first word occurs in text.
func precedingAdjective(text, surface string) string {
	firstWord := strings.Fields(surface)[0]
	tokens := tokenizeWords(text)
	for i, token := range tokens {
		if strings.EqualFold(token, firstWord) && i > 0 {
			for j := i - 1; j >= 0; j-- {
				lower := strings.ToLower(strings.Trim(tokens[j], ".,;:!?\"'"))
				switch lower {
				case "a", "an", "the", "of", "from", "in", "on", "with":
					continue
				}
				return strings.Trim(tokens[j], ".,;:!?\"'")
			}
		}
	}
	return ""
}

// querySurface is the text used inside a query (QueryName overrides Text).
func (e ResolvedEntity) querySurface() string {
	if e.QueryName != "" {
		return e.QueryName
	}
	return e.Text
}

// ── Small helpers ─────────────────────────────────────────────────────

// runeOffset returns the rune offset of the first occurrence of surface in
// text (used only for deterministic ordering).
func runeOffset(text, surface string) int {
	idx := strings.Index(text, surface)
	if idx < 0 {
		return 0
	}
	return len([]rune(text[:idx]))
}

// tokenizeWords splits text into whitespace-separated word tokens.
func tokenizeWords(text string) []string {
	return strings.Fields(text)
}

// uniqueStrings dedups strings preserving first-occurrence order.
func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
