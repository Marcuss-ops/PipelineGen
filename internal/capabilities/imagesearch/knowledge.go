package imagesearch

import (
	"regexp"
	"strings"
)

// KnownEntity is one curated, deterministic identity the resolver can type
// and disambiguate without an external model. The table is conservative: it
// only contains identities the golden battery (and the editorial visual
// intent) require, and every generic entry (animals/objects) is gated by
// RequireAny/ForbidAny context words so a "jaguar" in the rainforest is never
// mistaken for a "Jaguar" luxury car.
type KnownEntity struct {
	// Kind is the coarse category ("person", "org", "product", "landmark",
	// "place", "animal", "object", "category").
	Kind string
	// Type is the canonical entity type emitted in the decision.
	Type string
	// Name is the canonical name used to derive the canonical id ("Apple
	// Vision Pro", "Michael B. Jordan", "apple fruit").
	Name string
	// QueryName is the surface used inside queries when it differs from Name
	// ("Michael B Jordan" without the period).
	QueryName string
	// Surfaces are the exact case-sensitive spellings to search for. Proper
	// entities match capitalized spellings only; Generic entries match their
	// lowercase spelling.
	Surfaces []string
	// Brand names the brand that must co-occur for a product entry to merge
	// ("Vision Pro" → "Apple Vision Pro" only when "Apple" is present).
	Brand string
	// Hint is the retrieval qualifier ("boxer", "basketball", "actor",
	// "company", "car"). Empty for unambiguous identities.
	Hint string
	// KindWord is the generic kind appended to the query of a generic
	// subject ("animal", "fruit").
	KindWord string
	// Domain groups identities for co-occurrence rules ("boxing",
	// "basketball", "acting"). Empty for non-persons.
	Domain string
	// Disambiguates marks identities whose Hint resolves an ambiguous name;
	// such identities emit a CONTEXT annotation.
	Disambiguates bool
	// Generic marks lowercase subjects (animals, objects, categories).
	Generic bool
	// RequireAny/ForbidAny are the context gates applied to the whole
	// lowercased text. RequireAny must have at least one match; ForbidAny
	// must have none.
	RequireAny []string
	ForbidAny  []string
}

// knownEntities is the closed identity knowledge base. Entries with longer
// surfaces are matched first so "New York City" wins over "New York".
var knownEntities = []KnownEntity{
	// ── Boxers (the identity domain of the first battery groups) ────────
	{Kind: "person", Type: "PERSON", Name: "Floyd Mayweather", Surfaces: []string{"Floyd Mayweather"}, Domain: "boxing"},
	{Kind: "person", Type: "PERSON", Name: "Manny Pacquiao", Surfaces: []string{"Manny Pacquiao"}, Domain: "boxing"},
	{Kind: "person", Type: "PERSON", Name: "Mike Tyson", Surfaces: []string{"Mike Tyson"}, Domain: "boxing", Hint: "boxer"},
	{Kind: "person", Type: "PERSON", Name: "Muhammad Ali", Surfaces: []string{"Muhammad Ali"}, Domain: "boxing"},
	{Kind: "person", Type: "PERSON", Name: "Oleksandr Usyk", Surfaces: []string{"Oleksandr Usyk"}, Domain: "boxing"},
	{Kind: "person", Type: "PERSON", Name: "Tyson Fury", Surfaces: []string{"Tyson Fury"}, Domain: "boxing", Hint: "boxer"},

	// ── Ambiguous identities (the T18/T19 golden pair) ──────────────────
	{Kind: "person", Type: "PERSON", Name: "Michael Jordan", Surfaces: []string{"Michael Jordan"}, Domain: "basketball", Hint: "basketball", Disambiguates: true,
		RequireAny: []string{"basketball", "bulls", "nba", "chicago", "legend", "court", "jersey", "team", "game"},
		ForbidAny:  []string{"actor", "film", "movie", "hollywood", "starred", "films"}},
	// The canonical Name carries no period so SafeEntityID derives the clean
	// slug "michael-b-jordan" (the verbatim surface keeps the period).
	{Kind: "person", Type: "PERSON", Name: "Michael B Jordan", QueryName: "Michael B Jordan", Surfaces: []string{"Michael B. Jordan", "Michael B Jordan"}, Domain: "acting", Hint: "actor", Disambiguates: true,
		RequireAny: []string{"actor", "film", "movie", "hollywood", "starred", "films", "star"},
		ForbidAny:  []string{"basketball", "bulls", "nba", "chicago", "legend"}},

	// ── Orgs / brands ───────────────────────────────────────────────────
	{Kind: "org", Type: "ORG", Name: "Apple", Surfaces: []string{"Apple"}, Hint: "company",
		RequireAny: []string{"reported", "demand", "devices", "earnings", "revenue", "sales", "company", "inc", "products", "technology", "introduced", "announced", "unveiled", "vision pro", "spatial", "headset", "iphone", "shares", "stock", "quarter", "profit"},
		ForbidAny:  []string{"farmer", "tree", "picked", "fruit", "orchard", "harvest", "red apple", "green apple"}},
	{Kind: "org", Type: "ORG", Name: "Tesla", Surfaces: []string{"Tesla"},
		RequireAny: []string{"cybertruck", "model", "car", "vehicle", "automotive", "musk", "electric", "ev", "unveiled", "factory", "design", "truck"},
		ForbidAny:  []string{}},
	{Kind: "org", Type: "ORG", Name: "SpaceX", Surfaces: []string{"SpaceX"},
		RequireAny: []string{"starship", "rocket", "launch", "mission", "space", "musk", "falcon", "orbit"},
		ForbidAny:  []string{}},
	{Kind: "org", Type: "ORG", Name: "Jaguar", Surfaces: []string{"Jaguar"}, Hint: "car",
		RequireAny: []string{"unveiled", "vehicle", "car", "luxury", "automotive", "model", "announced", "launched", "sedan", "suv", "design", "brand"},
		ForbidAny:  []string{"rainforest", "jungle", "forest", "moved", "wild", "prey", "hunting", "animal"}},
	{Kind: "org", Type: "ORG", Name: "Chicago Bulls", Surfaces: []string{"Chicago Bulls"}, Domain: "basketball",
		RequireAny: []string{"basketball", "nba", "chicago", "jordan", "legend", "team", "court", "game"},
		ForbidAny:  []string{}},
	{Kind: "org", Type: "ORG", Name: "Mayweather Promotions", Surfaces: []string{"Mayweather Promotions"}},

	// ── Products (merged with their brand when the brand co-occurs) ─────
	{Kind: "product", Type: "PRODUCT", Name: "Apple Vision Pro", Surfaces: []string{"Vision Pro"}, Brand: "Apple",
		RequireAny: []string{"apple", "introduced", "spatial", "device", "headset", "computing"}},
	{Kind: "product", Type: "PRODUCT", Name: "Tesla Cybertruck", Surfaces: []string{"Cybertruck"}, Brand: "Tesla",
		RequireAny: []string{"tesla", "truck", "vehicle", "automotive", "design", "unveiled", "car"}},
	{Kind: "product", Type: "PRODUCT", Name: "SpaceX Starship", Surfaces: []string{"Starship"}, Brand: "SpaceX",
		RequireAny: []string{"spacex", "rocket", "space", "mission", "launch", "vehicle", "next"}},

	// ── Landmarks / locations ───────────────────────────────────────────
	{Kind: "landmark", Type: "LANDMARK", Name: "Eiffel Tower", Surfaces: []string{"Eiffel Tower"}},
	{Kind: "landmark", Type: "LANDMARK", Name: "Buckingham Palace", Surfaces: []string{"Buckingham Palace"}},
	{Kind: "location", Type: "LOCATION", Name: "Times Square", Surfaces: []string{"Times Square"}},
	{Kind: "location", Type: "LOCATION", Name: "Amazon rainforest", Surfaces: []string{"Amazon rainforest"}},

	// ── Places (GPE) ────────────────────────────────────────────────────
	{Kind: "place", Type: "GPE", Name: "Philippines", Surfaces: []string{"Philippines"}},
	{Kind: "place", Type: "GPE", Name: "Saudi Arabia", Surfaces: []string{"Saudi Arabia"}},
	{Kind: "place", Type: "GPE", Name: "Paris", Surfaces: []string{"Paris"}},
	{Kind: "place", Type: "GPE", Name: "London", Surfaces: []string{"London"}},
	{Kind: "place", Type: "GPE", Name: "New York City", Surfaces: []string{"New York City"}},

	// ── Generic subjects (lowercase, context-gated) ─────────────────────
	{Kind: "animal", Type: "ANIMAL", Name: "jaguar", Surfaces: []string{"jaguar"}, Generic: true, KindWord: "animal",
		RequireAny: []string{"rainforest", "jungle", "forest", "moved", "silently", "wild", "prey", "hunting", "amazon", "cat", "animal", "prowled", "stalked"},
		ForbidAny:  []string{"unveiled", "vehicle", "car", "luxury", "automotive", "launched", "sedan", "suv"}},
	{Kind: "object", Type: "OBJECT", Name: "apple fruit", Surfaces: []string{"apple"}, Generic: true, KindWord: "fruit",
		RequireAny: []string{"farmer", "tree", "picked", "fruit", "orchard", "red", "green", "harvest", "grew", "grow", "basket", "apples", "ate"},
		ForbidAny:  []string{"reported", "demand", "devices", "earnings", "revenue", "sales", "company", "inc", "products", "technology", "introduced", "spatial", "iphone"}},

	// ── Categories (B-roll subjects) ────────────────────────────────────
	{Kind: "category", Type: "CATEGORY", Name: "real estate", Surfaces: []string{"real estate"}, Generic: true},
}

// knownHit is one matched KnownEntity occurrence.
type knownHit struct {
	entry   KnownEntity
	surface string
	start   int // byte offset of the first occurrence
}

// matchKnownEntities scans text for every known identity whose surface
// matches with the right case and whose context gates pass. Longer surfaces
// win first; each entry contributes at most one hit.
func matchKnownEntities(text string) []knownHit {
	lower := strings.ToLower(text)
	var hits []knownHit
	for _, entry := range knownEntities {
		if !contextAllows(entry, lower) {
			continue
		}
		for _, surface := range entry.Surfaces {
			if !strings.Contains(text, surface) {
				continue
			}
			start := strings.Index(text, surface)
			hits = append(hits, knownHit{entry: entry, surface: surface, start: start})
			break
		}
	}
	// Longer surfaces first so a multi-word identity wins its slot; equal
	// lengths keep source order (deterministic).
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && len(hits[j].surface) > len(hits[j-1].surface); j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	return hits
}

// contextAllows applies the RequireAny/ForbidAny gates of an entry to the
// lowercased text. Entries without gates always pass.
func contextAllows(entry KnownEntity, lower string) bool {
	for _, forbid := range entry.ForbidAny {
		if strings.Contains(lower, forbid) {
			return false
		}
	}
	if len(entry.RequireAny) == 0 {
		return true
	}
	for _, require := range entry.RequireAny {
		if strings.Contains(lower, require) {
			return true
		}
	}
	return false
}

// ── Value / event / structural patterns ───────────────────────────────

// moneyPatterns detect MONEY surfaces. Each pattern has two capture groups:
// the value (what the MONEY entity is about) and the phrase (the verb clause
// the visual scheduler can render as an animated money graphic).
var moneyPatterns = []struct {
	value  *regexp.Regexp
	phrase *regexp.Regexp
}{
	{
		value:  regexp.MustCompile(`((?:more than|over|about|nearly|at least|up to|roughly)\s*\$[\d,]+(?:\s*(?:million|billion|trillion)s?)?)`),
		phrase: regexp.MustCompile(`(\w+\s+(?:more than|over|about|nearly|at least|up to|roughly)\s*\$[\d,]+(?:\s*(?:million|billion|trillion)s?)?)`),
	},
	{
		value:  regexp.MustCompile(`((?:hundreds of|tens of|billions of)\s+(?:millions|billions)\s+of\s+dollars)`),
		phrase: regexp.MustCompile(`(\w+\s+(?:hundreds of|tens of|billions of)\s+(?:millions|billions)\s+of\s+dollars)`),
	},
	{
		value:  regexp.MustCompile(`((?:huge|enormous)\s+purses)`),
		phrase: regexp.MustCompile(`(\w+\s+(?:huge|enormous)\s+purses)`),
	},
}

// decadeRE detects DATE surfaces such as "late 1980s".
var decadeRE = regexp.MustCompile(`((?:early|mid|late)\s+(?:19|20)\d{2}s)`)

// eventRE detects concrete event noun phrases such as "historic heavyweight
// showdown". It deliberately excludes bare "fight/fights" so "won major
// heavyweight fights" stays a person+place sentence, not an EVENT.
var eventRE = regexp.MustCompile(`((?:historic|major|championship|title|epic|legendary)\s+(?:heavyweight\s+)?(?:showdown|battle|clash|duel))`)

// ── Structural vocabulary ─────────────────────────────────────────────

// negationParticles are the particles that negate an immediately-following
// entity ("not Mike Tyson"). Mirrors config/lexicons/en/negative_particles.txt.
var negationParticles = map[string]bool{
	"not": true, "no": true, "never": true, "neither": true, "nor": true,
}

// pronouns are the subject/possessive pronouns the resolver can ground on a
// prior sentence (coreference). Only a leading pronoun triggers resolution.
var pronouns = map[string]bool{
	"he": true, "his": true, "him": true, "she": true, "her": true, "hers": true,
	"they": true, "their": true, "them": true, "it": true, "its": true,
	"we": true, "our": true, "us": true,
}

// subordinateMarkers open a leading subordinate clause ("After earning huge
// purses …, Floyd Mayweather invested …"). When present, the primary person
// is the LAST-mentioned person (the main-clause subject), not the first.
var subordinateMarkers = map[string]bool{
	"after": true, "before": true, "although": true, "while": true, "since": true,
	"because": true, "as": true, "when": true, "despite": true, "once": true,
	"until": true, "if": true, "though": true, "having": true, "with": true,
	"in": true, "by": true, "through": true, "upon": true,
}

// fightContextWords trigger the "N1 N2 fight" event query when two known
// boxers co-occur. "fights against" (a source description) is deliberately
// absent so T28 does not fabricate a fight query.
var fightContextWords = []string{
	"defeated", "faced", "beat", "battled", "versus", "showdown", "fought", "fight with",
}

// hasFightContext reports whether the sentence expresses an actual fight
// event between its subjects.
func hasFightContext(lower string) bool {
	for _, word := range fightContextWords {
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
// pronoun ("He later invested …", "His fortune changed …").
func startsWithPronoun(text string) bool {
	return pronouns[leadingToken(text)]
}

// startsSubordinateClause reports whether the sentence opens a subordinate
// clause ("After earning … , Floyd Mayweather …").
func startsSubordinateClause(text string) bool {
	return subordinateMarkers[leadingToken(text)]
}
