package imagesearch

import "strings"

func negationPhrasesFor(lang string) [][]string {
	if lang == "it" {
		return negationPhrasesIT
	}
	return negationPhrases
}

// matchAlias returns the longest alias descriptor that opens the sentence,
// or nil when none does ("The fighter later invested …" → the fighter;
// "Il pugile in seguito …" → il pugile). Only the leading position is
// considered: like pronoun coreference, an alias is the sentence SUBJECT.
func matchAlias(text string, lang string) *aliasDescriptor {
	tokens := tokenizeWords(text)
	if len(tokens) == 0 {
		return nil
	}
	table := aliases
	if lang == "it" {
		table = aliasesIT
	}
	var best *aliasDescriptor
	bestLen := 0
	for i := range table {
		words := strings.Fields(table[i].surface)
		if len(words) == 0 || len(words) > len(tokens) {
			continue
		}
		ok := true
		for k, w := range words {
			if !strings.EqualFold(strings.Trim(tokens[k], ".,;:!?\"'"), w) {
				ok = false
				break
			}
		}
		if ok && len(words) > bestLen {
			best = &table[i]
			bestLen = len(words)
		}
	}
	return best
}

// knownEntityByName returns the KB entry for a (canonical) person name,
// matched case-insensitively on Name or any language surface, or nil.
func knownEntityByName(name string) *KnownEntity {
	lower := strings.ToLower(strings.TrimSpace(name))
	for i := range knownEntities {
		entry := &knownEntities[i]
		if strings.EqualFold(lower, strings.ToLower(strings.TrimSpace(entry.Name))) {
			return entry
		}
		for _, s := range entry.Surfaces {
			if strings.EqualFold(lower, strings.ToLower(strings.TrimSpace(s))) {
				return entry
			}
		}
		for _, s := range entry.SurfacesIT {
			if strings.EqualFold(lower, strings.ToLower(strings.TrimSpace(s))) {
				return entry
			}
		}
	}
	return nil
}

// knownDomain reports whether the (canonical) person name is a known
// identity of the given domain in the knowledge base.
func knownDomain(name string, domain string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, entry := range knownEntities {
		if entry.Domain != domain {
			continue
		}
		if strings.EqualFold(lower, strings.ToLower(strings.TrimSpace(entry.Name))) {
			return true
		}
		for _, s := range entry.Surfaces {
			if strings.EqualFold(lower, strings.ToLower(strings.TrimSpace(s))) {
				return true
			}
		}
		for _, s := range entry.SurfacesIT {
			if strings.EqualFold(lower, strings.ToLower(strings.TrimSpace(s))) {
				return true
			}
		}
	}
	return false
}

// resolvePriorPerson picks the prior person a pronoun or alias grounds on.
// Domain-specific aliases only resolve to a prior person that is a known
// identity of that domain ("the fighter" → a known boxer, never Steve
// Jobs); pronouns and generic aliases resolve to the most recent prior
// person. Returns "" when nothing is resolvable.
func resolvePriorPerson(priors []string, alias *aliasDescriptor) string {
	for _, prior := range priors {
		prior = strings.TrimSpace(prior)
		if prior == "" {
			continue
		}
		if alias != nil && alias.domain != "" && !knownDomain(prior, alias.domain) {
			continue
		}
		return prior
	}
	return ""
}

func pronounsFor(lang string) map[string]bool {
	if lang == "it" {
		return pronounsIT
	}
	return pronouns
}

func subordinateMarkersFor(lang string) map[string]bool {
	if lang == "it" {
		return subordinateMarkersIT
	}
	return subordinateMarkers
}

func fightContextWordsFor(lang string) []string {
	if lang == "it" {
		return fightContextWordsIT
	}
	return fightContextWords
}

func moneyPatternsFor(lang string) []moneyPattern {
	if lang == "it" {
		return moneyPatternsIT
	}
	return moneyPatterns
}

// hasFightContext reports whether the sentence expresses an actual fight
// event between its subjects, for the request language.
func hasFightContext(lower string, lang string) bool {
	for _, word := range fightContextWordsFor(lang) {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// leadingToken returns the lowercased first word of the text, punctuation
// trimmed.
func leadingToken(text string) string {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(strings.Trim(fields[0], ".,;:!?\"'"))
}

// startsWithPronoun reports whether the sentence opens with a resolvable
// pronoun ("He later invested …", "His fortune changed …", "La sua fortuna
// …", "Lui in seguito …"), for the request language.
func startsWithPronoun(text string, lang string) bool {
	if lang == "it" {
		return startsWithPronounIT(text)
	}
	return pronouns[leadingToken(text)]
}

// startsWithPronounIT handles the Italian pronoun openers: a bare subject /
// possessive pronoun ("Lui", "Suo") OR the article+possessive pattern ("Il
// suo", "La sua", "I suoi", "Le sue").
func startsWithPronounIT(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}
	first := strings.ToLower(strings.Trim(fields[0], ".,;:!?\"'"))
	if pronounsIT[first] {
		return true
	}
	if len(fields) >= 2 {
		second := strings.ToLower(strings.Trim(fields[1], ".,;:!?\"'"))
		if italianArticles[first] && italianPossessives[second] {
			return true
		}
	}
	return false
}

// startsSubordinateClause reports whether the sentence opens a subordinate
// clause ("After earning … , Floyd Mayweather …", "Dopo aver guadagnato …"),
// for the request language.
func startsSubordinateClause(text string, lang string) bool {
	return subordinateMarkersFor(lang)[leadingToken(text)]
}
