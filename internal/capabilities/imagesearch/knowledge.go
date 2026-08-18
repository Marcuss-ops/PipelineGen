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

	// SurfacesIT are the Italian spellings of the identity ("Torre Eiffel",
	// "giaguaro", "Arabia Saudita"). When set and the request language is
	// Italian they REPLACE Surfaces; the canonical Name/QueryName stay
	// unchanged, so Italian input canonicalizes to the same visual intent
	// the English battery certifies.
	SurfacesIT []string
	// RequireAnyIT/ForbidAnyIT override the context gates for Italian text
	// ("basket" vs "pallacanestro", "attore" vs "actor"). When unset the
	// English gates are used.
	RequireAnyIT []string
	ForbidAnyIT  []string
}

// surfacesFor returns the surfaces to match for the request language.
func surfacesFor(entry KnownEntity, lang string) []string {
	if lang == "it" && len(entry.SurfacesIT) > 0 {
		return entry.SurfacesIT
	}
	return entry.Surfaces
}

// requireAnyFor returns the RequireAny gate for the request language.
func requireAnyFor(entry KnownEntity, lang string) []string {
	if lang == "it" && len(entry.RequireAnyIT) > 0 {
		return entry.RequireAnyIT
	}
	return entry.RequireAny
}

// forbidAnyFor returns the ForbidAny gate for the request language.
func forbidAnyFor(entry KnownEntity, lang string) []string {
	if lang == "it" && len(entry.ForbidAnyIT) > 0 {
		return entry.ForbidAnyIT
	}
	return entry.ForbidAny
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
		ForbidAny:  []string{"actor", "film", "movie", "hollywood", "starred", "films"},
		RequireAnyIT: []string{"basket", "bulls", "nba", "chicago", "leggenda", "campo", "maglia", "squadra", "partita", "pallacanestro", "giocatore"},
		ForbidAnyIT:  []string{"attore", "film", "cinema", "hollywood", "recitato", "pellicole", "interpretato"}},
	// The canonical Name carries no period so SafeEntityID derives the clean
	// slug "michael-b-jordan" (the verbatim surface keeps the period).
	{Kind: "person", Type: "PERSON", Name: "Michael B Jordan", QueryName: "Michael B Jordan", Surfaces: []string{"Michael B. Jordan", "Michael B Jordan"}, Domain: "acting", Hint: "actor", Disambiguates: true,
		RequireAny: []string{"actor", "film", "movie", "hollywood", "starred", "films", "star"},
		ForbidAny:  []string{"basketball", "bulls", "nba", "chicago", "legend"},
		RequireAnyIT: []string{"attore", "film", "cinema", "hollywood", "recitato", "pellicole", "interpretato", "star"},
		ForbidAnyIT:  []string{"basket", "bulls", "nba", "chicago", "leggenda", "pallacanestro", "squadra", "partita"}},

	// ── Orgs / brands ───────────────────────────────────────────────────
	{Kind: "org", Type: "ORG", Name: "Apple", Surfaces: []string{"Apple"}, Hint: "company",
		RequireAny: []string{"reported", "demand", "devices", "earnings", "revenue", "sales", "company", "inc", "products", "technology", "introduced", "announced", "unveiled", "vision pro", "spatial", "headset", "iphone", "shares", "stock", "quarter", "profit"},
		ForbidAny:  []string{"farmer", "tree", "picked", "fruit", "orchard", "harvest", "red apple", "green apple"},
		RequireAnyIT: []string{"riportato", "domanda", "dispositivi", "ricavi", "vendite", "azienda", "inc", "prodotti", "tecnologia", "introdotto", "annunciato", "svelato", "vision pro", "spaziale", "cuffia", "iphone", "azioni", "titolo", "trimestre", "utile", "elaborazione"},
		ForbidAnyIT:  []string{"contadino", "albero", "raccolto", "frutto", "frutteto", "mela rossa", "mela verde", "mele", "cestino", "raccolta"}},
	{Kind: "org", Type: "ORG", Name: "Tesla", Surfaces: []string{"Tesla"},
		RequireAny: []string{"cybertruck", "model", "car", "vehicle", "automotive", "musk", "electric", "ev", "unveiled", "factory", "design", "truck"},
		ForbidAny:  []string{},
		RequireAnyIT: []string{"cybertruck", "modello", "auto", "veicolo", "automobilistico", "musk", "elettrica", "elettrico", "svelato", "presentato", "fabbrica", "design", "camion", "veicoli", "automotive"},
		ForbidAnyIT:  []string{}},
	{Kind: "org", Type: "ORG", Name: "SpaceX", Surfaces: []string{"SpaceX"},
		RequireAny: []string{"starship", "rocket", "launch", "mission", "space", "musk", "falcon", "orbit"},
		ForbidAny:  []string{},
		RequireAnyIT: []string{"starship", "razzo", "lancio", "missione", "missioni", "spazio", "spaziali", "musk", "falcon", "orbita", "sviluppato"},
		ForbidAnyIT:  []string{}},
	{Kind: "org", Type: "ORG", Name: "Jaguar", Surfaces: []string{"Jaguar"}, Hint: "car",
		RequireAny: []string{"unveiled", "vehicle", "car", "luxury", "automotive", "model", "announced", "launched", "sedan", "suv", "design", "brand"},
		ForbidAny:  []string{"rainforest", "jungle", "forest", "moved", "wild", "prey", "hunting", "animal"},
		RequireAnyIT: []string{"svelato", "veicolo", "auto", "lusso", "automotive", "modello", "annunciato", "lanciato", "berlina", "suv", "design", "marchio", "presentato"},
		ForbidAnyIT:  []string{"foresta", "giungla", "selva", "selvaggio", "preda", "caccia", "animale", "amazzonia", "amazzonica", "si muoveva", "silenziosamente", "felino"}},
	{Kind: "org", Type: "ORG", Name: "Chicago Bulls", Surfaces: []string{"Chicago Bulls"}, Domain: "basketball",
		RequireAny: []string{"basketball", "nba", "chicago", "jordan", "legend", "team", "court", "game"},
		ForbidAny:  []string{},
		RequireAnyIT: []string{"basket", "nba", "chicago", "jordan", "leggenda", "squadra", "campo", "partita", "pallacanestro"},
		ForbidAnyIT:  []string{}},
	{Kind: "org", Type: "ORG", Name: "Mayweather Promotions", Surfaces: []string{"Mayweather Promotions"}},

	// ── Products (merged with their brand when the brand co-occurs) ─────
	{Kind: "product", Type: "PRODUCT", Name: "Apple Vision Pro", Surfaces: []string{"Vision Pro"}, Brand: "Apple",
		RequireAny: []string{"apple", "introduced", "spatial", "device", "headset", "computing"},
		RequireAnyIT: []string{"apple", "introdotto", "spaziale", "dispositivo", "cuffia", "elaborazione", "computing"}},
	{Kind: "product", Type: "PRODUCT", Name: "Tesla Cybertruck", Surfaces: []string{"Cybertruck"}, Brand: "Tesla",
		RequireAny: []string{"tesla", "truck", "vehicle", "automotive", "design", "unveiled", "car"},
		RequireAnyIT: []string{"tesla", "camion", "veicolo", "automobilistico", "design", "svelato", "presentato", "auto"}},
	{Kind: "product", Type: "PRODUCT", Name: "SpaceX Starship", Surfaces: []string{"Starship"}, Brand: "SpaceX",
		RequireAny: []string{"spacex", "rocket", "space", "mission", "launch", "vehicle", "next"},
		RequireAnyIT: []string{"spacex", "razzo", "spazio", "missione", "lancio", "veicolo", "prossima", "generazione", "sviluppato"}},

	// ── Landmarks / locations ───────────────────────────────────────────
	{Kind: "landmark", Type: "LANDMARK", Name: "Eiffel Tower", Surfaces: []string{"Eiffel Tower"}, SurfacesIT: []string{"Torre Eiffel"}},
	{Kind: "landmark", Type: "LANDMARK", Name: "Buckingham Palace", Surfaces: []string{"Buckingham Palace"}},
	{Kind: "location", Type: "LOCATION", Name: "Times Square", Surfaces: []string{"Times Square"}},
	{Kind: "location", Type: "LOCATION", Name: "Amazon rainforest", Surfaces: []string{"Amazon rainforest"},
		SurfacesIT: []string{"foresta amazzonica", "Foresta amazzonica", "Amazzonia", "amazzonia"}},

	// ── Places (GPE) ────────────────────────────────────────────────────
	{Kind: "place", Type: "GPE", Name: "Philippines", Surfaces: []string{"Philippines"}, SurfacesIT: []string{"Filippine"}},
	{Kind: "place", Type: "GPE", Name: "Saudi Arabia", Surfaces: []string{"Saudi Arabia"}, SurfacesIT: []string{"Arabia Saudita"}},
	{Kind: "place", Type: "GPE", Name: "Paris", Surfaces: []string{"Paris"}, SurfacesIT: []string{"Parigi"}},
	{Kind: "place", Type: "GPE", Name: "London", Surfaces: []string{"London"}, SurfacesIT: []string{"Londra"}},
	{Kind: "place", Type: "GPE", Name: "New York City", Surfaces: []string{"New York City"}, SurfacesIT: []string{"New York"}},

	// ── Generic subjects (lowercase, context-gated) ─────────────────────
	{Kind: "animal", Type: "ANIMAL", Name: "jaguar", Surfaces: []string{"jaguar"}, Generic: true, KindWord: "animal",
		RequireAny: []string{"rainforest", "jungle", "forest", "moved", "silently", "wild", "prey", "hunting", "amazon", "cat", "animal", "prowled", "stalked"},
		ForbidAny:  []string{"unveiled", "vehicle", "car", "luxury", "automotive", "launched", "sedan", "suv"},
		SurfacesIT: []string{"giaguaro"},
		RequireAnyIT: []string{"foresta", "amazzonia", "amazzonica", "si muoveva", "silenziosamente", "selvaggio", "preda", "caccia", "felino", "animale", "giungla", "selva", "appostato", "braccato", "attraversava"},
		ForbidAnyIT:  []string{"svelato", "veicolo", "auto", "lusso", "automotive", "lanciato", "berlina", "suv", "marchio", "modello", "presentato"}},
	{Kind: "object", Type: "OBJECT", Name: "apple fruit", Surfaces: []string{"apple"}, Generic: true, KindWord: "fruit",
		RequireAny: []string{"farmer", "tree", "picked", "fruit", "orchard", "red", "green", "harvest", "grew", "grow", "basket", "apples", "ate"},
		ForbidAny:  []string{"reported", "demand", "devices", "earnings", "revenue", "sales", "company", "inc", "products", "technology", "introduced", "spatial", "iphone"},
		SurfacesIT: []string{"mela", "mele"},
		RequireAnyIT: []string{"contadino", "albero", "raccolto", "raccolta", "frutto", "frutteto", "mela rossa", "rosso", "rossa", "verde", "cestino", "mangiato", "mele"},
		ForbidAnyIT:  []string{"riportato", "domanda", "dispositivi", "ricavi", "vendite", "azienda", "inc", "prodotti", "tecnologia", "introdotto", "spaziale", "iphone", "azioni", "trimestre", "utile"}},

	// ── Categories (B-roll subjects) ────────────────────────────────────
	{Kind: "category", Type: "CATEGORY", Name: "real estate", Surfaces: []string{"real estate"}, Generic: true,
		SurfacesIT: []string{"settore immobiliare", "immobiliare", "proprietà immobiliari", "beni immobili"}},
}

// knownHit is one matched KnownEntity occurrence.
type knownHit struct {
	entry   KnownEntity
	surface string
	start   int // byte offset of the first occurrence
}

// matchKnownEntities scans text for every known identity whose surface
// matches with the right case (for the request language) and whose context
// gates pass. Longer surfaces win first; each entry contributes at most one
// hit.
func matchKnownEntities(text string, lang string) []knownHit {
	lower := strings.ToLower(text)
	var hits []knownHit
	for _, entry := range knownEntities {
		if !contextAllows(entry, lower, lang) {
			continue
		}
		for _, surface := range surfacesFor(entry, lang) {
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

// contextAllows applies the RequireAny/ForbidAny gates of an entry (for the
// request language) to the lowercased text. Entries without gates always
// pass.
func contextAllows(entry KnownEntity, lower string, lang string) bool {
	for _, forbid := range forbidAnyFor(entry, lang) {
		if strings.Contains(lower, forbid) {
			return false
		}
	}
	requires := requireAnyFor(entry, lang)
	if len(requires) == 0 {
		return true
	}
	for _, require := range requires {
		if strings.Contains(lower, require) {
			return true
		}
	}
	return false
}

// ── Value / event / structural patterns ───────────────────────────────

// moneyPattern is one MONEY detection rule: value captures the MONEY entity
// (what the visual system renders), phrase captures the verb clause (the
// editorial surface for the visual scheduler).
type moneyPattern struct {
	value  *regexp.Regexp
	phrase *regexp.Regexp
}

// moneyPatterns detect English MONEY surfaces.
var moneyPatterns = []moneyPattern{
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

// moneyPatternsIT detect Italian MONEY surfaces ("più di 100 milioni di
// dollari", "centinaia di milioni di dollari", "enormi borse da
// combattimento").
var moneyPatternsIT = []moneyPattern{
	{
		value:  regexp.MustCompile(`((?:più di|oltre|circa|quasi|almeno|fino a)\s*[\d.,]+\s*(?:milioni|miliardi)?\s*di\s*dollari)`),
		phrase: regexp.MustCompile(`(\w+\s+(?:più di|oltre|circa|quasi|almeno|fino a)\s*[\d.,]+\s*(?:milioni|miliardi)?\s*di\s*dollari)`),
	},
	{
		value:  regexp.MustCompile(`((?:centinaia di|decine di|miliardi di)\s+(?:milioni|miliardi)\s+di\s+dollari)`),
		phrase: regexp.MustCompile(`(\w+\s+(?:centinaia di|decine di|miliardi di)\s+(?:milioni|miliardi)\s+di\s+dollari)`),
	},
	{
		value:  regexp.MustCompile(`((?:enormi|ingenti)\s+borse(?:\s+da\s+combattimento)?)`),
		phrase: regexp.MustCompile(`(\w+\s+(?:enormi|ingenti)\s+borse(?:\s+da\s+combattimento)?)`),
	},
}

// decadeRE detects English DATE surfaces such as "late 1980s".
var decadeRE = regexp.MustCompile(`((?:early|mid|late)\s+(?:19|20)\d{2}s)`)

// decadeREIT detects Italian DATE surfaces such as "fine degli anni '80".
var decadeREIT = regexp.MustCompile(`((?:(?:inizio|metà|fine)\s+degli\s+)?anni\s+['’]?\d{2})`)

// eventRE detects concrete English event noun phrases such as "historic
// heavyweight showdown". It deliberately excludes bare "fight/fights" so
// "won major heavyweight fights" stays a person+place sentence, not an EVENT.
var eventRE = regexp.MustCompile(`((?:historic|major|championship|title|epic|legendary)\s+(?:heavyweight\s+)?(?:showdown|battle|clash|duel))`)

// eventREIT detects concrete Italian event noun phrases such as "storico
// scontro dei pesi massimi". It deliberately excludes "combattimento/i" so
// "importanti combattimenti dei pesi massimi" stays a person+place sentence.
var eventREIT = regexp.MustCompile(`((?:storico|grande|importante|epico|leggendario)\s+(?:scontro|battaglia|clash|duello)(?:\s+dei\s+pesi\s+massimi)?)`)

// ── Structural vocabulary ─────────────────────────────────────────────

// negationParticles are the English particles that negate an
// immediately-following entity ("not Mike Tyson"). Mirrors
// config/lexicons/en/negative_particles.txt.
var negationParticles = map[string]bool{
	"not": true, "no": true, "never": true, "neither": true, "nor": true,
}

// negationParticlesIT are the Italian particles that negate an
// immediately-following entity ("non Mike Tyson"). Mirrors
// config/lexicons/it/negative_particles.txt.
var negationParticlesIT = map[string]bool{
	"non": true, "no": true, "mai": true, "ne": true, "né": true,
	"neanche": true, "nemmeno": true, "neppure": true,
}

// pronouns are the English subject/possessive pronouns the resolver can
// ground on a prior sentence (coreference). Only a leading pronoun triggers
// resolution.
var pronouns = map[string]bool{
	"he": true, "his": true, "him": true, "she": true, "her": true, "hers": true,
	"they": true, "their": true, "them": true, "it": true, "its": true,
	"we": true, "our": true, "us": true,
}

// pronounsIT are the Italian subject/possessive pronouns that can open a
// sentence and ground on a prior person (coreference): "Lui in seguito …",
// "Suo padre …".
var pronounsIT = map[string]bool{
	"lui": true, "lei": true, "egli": true, "ella": true, "essi": true, "esse": true,
	"esso": true, "essa": true, "loro": true, "suo": true, "sua": true,
	"suoi": true, "sue": true,
}

// italianArticles and italianPossessives support the "il suo / la sua / i
// suoi / le sue" possessive opener pattern ("La sua fortuna cambiò …").
var italianArticles = map[string]bool{
	"il": true, "lo": true, "la": true, "i": true, "gli": true, "le": true,
}

var italianPossessives = map[string]bool{
	"suo": true, "sua": true, "suoi": true, "sue": true,
}

// italianPronounClitics are the clitic/pronoun tokens that mark a pronoun
// reference anywhere in the sentence ("…, lo ha trasformato …").
var italianPronounClitics = map[string]bool{
	"lui": true, "lei": true, "egli": true, "ella": true, "essi": true, "esse": true,
	"esso": true, "essa": true, "loro": true, "lo": true, "la": true, "gli": true,
	"ne": true, "li": true, "le": true, "suo": true, "sua": true, "suoi": true, "sue": true,
}

// italianProDropAux are the third-person auxiliary/verb forms that mark a
// DROPPED subject in Italian ("Dopo aver guadagnato …, ha ampliato …" = "…
// he expanded …"). Only consulted when a leading subordinate clause is
// present AND a prior person exists, so "ha/è/sono" in ordinary sentences
// never fabricate an antecedent.
var italianProDropAux = map[string]bool{
	"ha": true, "hanno": true, "è": true, "sono": true, "era": true, "erano": true,
	"fu": true, "furono": true, "aveva": true, "avevano": true, "sarà": true,
	"saranno": true, "sta": true, "stavano": true, "stava": true, "avrebbe": true,
	"avrebbero": true, "sia": true, "fosse": true, "fossero": true,
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

// subordinateMarkersIT open a leading Italian subordinate clause ("Dopo aver
// guadagnato …, Floyd Mayweather ha investito …"; "In seguito ha investito
// …").
var subordinateMarkersIT = map[string]bool{
	"dopo": true, "prima": true, "sebbene": true, "benché": true, "mentre": true,
	"poiché": true, "perché": true, "quando": true, "nonostante": true,
	"finché": true, "se": true, "avendo": true, "in": true, "da": true,
	"con": true, "attraverso": true, "pur": true,
}

// fightContextWords trigger the "N1 N2 fight" event query when two known
// boxers co-occur. "fights against" (a source description) is deliberately
// absent so T28 does not fabricate a fight query.
var fightContextWords = []string{
	"defeated", "faced", "beat", "battled", "versus", "showdown", "fought", "fight with",
}

// fightContextWordsIT are the Italian fight-event triggers ("ha sconfitto",
// "ha affrontato"). "contro" (against) is deliberately absent so T28's
// "combattimento contro Manny Pacquiao" does not fabricate a fight query.
var fightContextWordsIT = []string{
	"sconfitto", "affrontato", "battuto", "sfidato", "sfidò", "sconfisse",
	"combatté", "sfida",
}

// ── Language accessors ────────────────────────────────────────────────

func negationParticlesFor(lang string) map[string]bool {
	if lang == "it" {
		return negationParticlesIT
	}
	return negationParticles
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
