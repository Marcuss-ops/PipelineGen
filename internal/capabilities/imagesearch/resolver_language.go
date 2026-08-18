package imagesearch

import (
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
)

func applyNegation(text string, entities []ResolvedEntity, lang string) ([]ResolvedEntity, []ResolvedEntity) {
	tokens := tokenizeWords(text)
	negatedIdx := map[int]bool{}
	for _, phrase := range negationPhrasesFor(lang) {
		for e := 0; e < len(tokens); e++ {
			if !phraseEndsAt(tokens, e, phrase) {
				continue
			}
			best, bestStart := -1, len(tokens)+1
			for idx, entity := range entities {
				start := entityTokenStart(tokens, entity.Text)
				if start < 0 || start <= e || start-e > 3 {
					continue
				}
				if start < bestStart {
					best, bestStart = idx, start
				}
			}
			if best >= 0 {
				negatedIdx[best] = true
			}
		}
	}
	var kept, negated []ResolvedEntity
	for idx, entity := range entities {
		if negatedIdx[idx] {
			negated = append(negated, entity)
		} else {
			kept = append(kept, entity)
		}
	}
	return kept, negated
}

// phraseEndsAt reports whether phrase occurs as a contiguous token run
// ending exactly at token index e ("instead of Mike Tyson": the phrase
// [instead of] ends at the token before "Mike").
func phraseEndsAt(tokens []string, e int, phrase []string) bool {
	n := len(phrase)
	start := e - n + 1
	if start < 0 {
		return false
	}
	trim := func(tok string) string { return strings.Trim(tok, ".,;:!?\"'()") }
	for k := 0; k < n; k++ {
		if !strings.EqualFold(trim(tokens[start+k]), phrase[k]) {
			return false
		}
	}
	return true
}

// entityTokenStart returns the token index where the entity's words first
// occur as a consecutive (case-insensitive, punctuation-trimmed) run, or -1.
func entityTokenStart(tokens []string, entityText string) int {
	words := strings.Fields(entityText)
	if len(words) == 0 {
		return -1
	}
	trim := func(tok string) string { return strings.Trim(tok, ".,;:!?\"'()") }
	for i := 0; i+len(words) <= len(tokens); i++ {
		ok := true
		for j, w := range words {
			if !strings.EqualFold(trim(tokens[i+j]), w) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// ── Coreference pass ──────────────────────────────────────────────────

// applyCoreference grounds a leading pronoun ("He later invested …"), a
// dropped Italian subject ("Dopo aver guadagnato …, ha ampliato …"), the
// article+possessive opener ("La sua fortuna …") or an alias descriptor
// ("The fighter later invested …" → the prior boxer) on a prior person.
// Domain-specific aliases only ground on a prior person of the right domain
// ("the fighter" never resolves to Steve Jobs). Without a supplied
// antecedent nothing is ever invented ("His fortune changed …" / "La sua
// fortuna …" / "The fighter …" with no prior → no entity).
func (r *Resolver) applyCoreference(text string, req Request, entities []ResolvedEntity, lang string) []ResolvedEntity {
	hasPronoun := startsWithPronoun(text, lang) || (startsSubordinateClause(text, lang) && sentenceHasPronoun(text, lang))
	alias := matchAlias(text, lang)
	if (!hasPronoun && alias == nil) || len(req.PriorPersons) == 0 {
		return entities
	}
	prior := resolvePriorPerson(req.PriorPersons, alias)
	if prior == "" {
		return entities
	}
	// The prior person may already be an explicit entity of this sentence
	// (e.g. a subordinate clause that names the subject AND carries a
	// pro-drop auxiliary: "Dopo … Floyd Mayweather ha investito …"). Never
	// duplicate the identity.
	for _, e := range entities {
		if strings.EqualFold(strings.TrimSpace(e.Text), strings.TrimSpace(prior)) {
			return entities
		}
	}
	entity := ResolvedEntity{
		Type:        "PERSON",
		Text:        prior,
		MentionAt:   runeOffset(text, leadingToken(text)),
		CanonicalID: capabilityentities.CanonicalEntityID("PERSON", prior),
	}
	// A known prior person carries its KB metadata, so an alias/pronoun
	// resolution queries like the identity itself ("Tyson Fury boxer", never
	// a bare name that could hit the wrong person).
	if entry := knownEntityByName(prior); entry != nil {
		entity.Hint = entry.Hint
		entity.Domain = entry.Domain
		entity.QueryName = entry.QueryName
	}
	entities = append(entities, entity)
	return entities
}

// sentenceHasPronoun reports whether the sentence contains a resolvable
// pronoun anywhere ("…, he expanded …", "…, lo ha trasformato …"). For
// Italian, a third-person auxiliary ("ha", "è", "sono") also marks a dropped
// subject, which is only meaningful when a leading subordinate clause is
// present (checked by the caller).
func sentenceHasPronoun(text string, lang string) bool {
	for _, token := range tokenizeWords(text) {
		tok := strings.ToLower(strings.Trim(token, ".,;:!?\"'"))
		if lang == "it" {
			if italianPronounClitics[tok] || italianProDropAux[tok] {
				return true
			}
			continue
		}
		if pronouns[tok] {
			return true
		}
	}
	return false
}
