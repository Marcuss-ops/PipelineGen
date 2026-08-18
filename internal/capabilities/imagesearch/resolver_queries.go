package imagesearch

import (
	"sort"
	"strings"
)

func personQuery(e ResolvedEntity, pair bool) string {
	if pair {
		return e.querySurface()
	}
	if e.Hint != "" {
		return e.querySurface() + " " + e.Hint
	}
	return e.querySurface()
}

func orgQuery(e ResolvedEntity) string {
	if e.Hint != "" {
		return e.querySurface() + " " + e.Hint
	}
	return e.querySurface()
}

// objectQuery folds the qualifying adjective into the query. English
// adjectives precede the noun ("red apple fruit"); Italian adjectives
// follow it ("una mela rossa" → "red apple fruit" via the canonical
// mapping). The canonical query is identical in both languages.
func objectQuery(e ResolvedEntity, text string, lang string) string {
	q := e.querySurface()
	if q == "" {
		return ""
	}
	if lang == "it" {
		if adj := followingAdjectiveIT(text, e.Verbatim); adj != "" {
			q = adj + " " + q
		}
		return q
	}
	if adj := precedingAdjective(text, e.Text); adj != "" {
		q = adj + " " + q
	}
	return q
}

// itAdjectiveMap maps the Italian adjective forms that immediately follow a
// generic object noun to the canonical English qualifier used in the
// (canonical) image query.
var itAdjectiveMap = map[string]string{
	"rosso": "red", "rossa": "red", "rossi": "red", "rosse": "red",
	"verde": "green", "verdi": "green",
	"giallo": "yellow", "gialla": "yellow", "gialli": "yellow", "gialle": "yellow",
}

// followingAdjectiveIT returns the mapped English adjective that directly
// follows the object noun in the Italian text ("mela rossa" → "red").
func followingAdjectiveIT(text, surface string) string {
	fields := strings.Fields(surface)
	if len(fields) == 0 {
		return ""
	}
	tokens := tokenizeWords(text)
	for i, token := range tokens {
		if !strings.EqualFold(strings.Trim(token, ".,;:!?\"'"), fields[0]) {
			continue
		}
		// In Italian the adjective directly follows the noun; stop at the
		// first non-adjective token ("una mela rossa dall'albero").
		for j := i + 1; j < len(tokens); j++ {
			lower := strings.ToLower(strings.Trim(tokens[j], ".,;:!?\"'"))
			if adj, ok := itAdjectiveMap[lower]; ok {
				return adj
			}
			break
		}
	}
	return ""
}

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

func precedingAdjective(text, surface string) string {
	fields := strings.Fields(surface)
	if len(fields) == 0 {
		return ""
	}
	tokens := tokenizeWords(text)
	for i, token := range tokens {
		if strings.EqualFold(token, fields[0]) && i > 0 {
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

func (e ResolvedEntity) querySurface() string {
	if e.QueryName != "" {
		return e.QueryName
	}
	return e.Text
}

func runeOffset(text, surface string) int {
	idx := strings.Index(text, surface)
	if idx < 0 {
		return 0
	}
	return len([]rune(text[:idx]))
}

func tokenizeWords(text string) []string { return strings.Fields(text) }

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

// entityPriority is the editorial priority for primary selection.
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

func primaryEntity(entities []ResolvedEntity, text, lower string, lang string) *ResolvedEntity {
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
		if startsWithPronoun(text, lang) {
			return &persons[0]
		}
		for _, p := range persons {
			if !strings.Contains(lower, strings.ToLower(p.Text)) {
				return &persons[0]
			}
		}
		if startsSubordinateClause(text, lang) {
			return &persons[len(persons)-1]
		}
		return &persons[0]
	}
	return &entities[0]
}
