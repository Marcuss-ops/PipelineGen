// Package imagesearch — golden_battery_test.go is the PERMANENT golden
// regression battery for Image Search. It certifies the chain
//
//	FRASE → Semantic/Entity Extraction → Canonicalizzazione
//	      → Image Search Query → (Relevance validation) → Immagine scelta
//
// with 28 sentences (T01–T28) designed to break extractor and resolver:
// persons, places, companies, products, multiple entities in one sentence,
// ambiguous names, negation, abstract sentences that must NOT trigger an
// image search, pronoun coreference, and the full multi-sentence paragraph
// PipelineGen will actually process.
//
// The battery runs the REAL deterministic CPU extractor (the production
// fallback) through the Resolver, and certifies, per sentence:
//
//	expected entities found (typed)      → entity recall / precision
//	canonical ids derived correctly      → canonicalization accuracy
//	primary + ordered queries            → query relevance
//	no-image decision on abstract text   → no-image decision accuracy
//	wrong identity / negated selection   → must be ZERO
//
// The golden regression pairs are T18/T19 (Michael Jordan vs Michael B.
// Jordan), T22/T23 (jaguar animal vs Jaguar car) and T27 (negation) — the
// tests that prove the resolver understands WHICH entity to represent
// visually, not just which words to extract.
//
// NOTE on canonical ids: the repo's canonical format (entities.CanonicalEntityID)
// is "{type}:{slug}" — e.g. "person:floyd-mayweather". The battery spec's
// shorthand "floyd-mayweather" is the slug part; the type prefix is the
// repo's SSOT and is asserted here.
package imagesearch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/linguistics"
	localnlp "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/nlp/local"
)

// TestMain installs the same repository lexicon the composition root loads,
// because the local NLP extractor resolves stop/function words through
// linguistics.DefaultLexicon(). No test-only word lists are allowed.
func TestMain(m *testing.M) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../config/lexicons"))
	registry, err := linguistics.NewLexiconRegistry(root)
	if err != nil {
		panic(err)
	}
	if err := linguistics.SetDefaultLexicon(registry); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// wantEntity is one expected typed entity. Canonical is asserted only when
// non-empty (value entities like MONEY carry no canonical expectation).
type wantEntity struct {
	typ       string
	text      string
	canonical string
}

// goldenCase is one battery sentence with its full expected surface.
type goldenCase struct {
	id             string
	text           string
	prior          []string
	wantRequired   bool
	wantQueries    []string // exact ordered query list (nil when no image search)
	wantEntities   []wantEntity
	wantNegated    []wantEntity
	wantVisual     []wantEntity
	wantContexts   []wantEntity
	forbidQueries  []string // must never appear in any query (case-insensitive)
	forbidEntities []wantEntity
	wantPhrases    []string // must be present in ImportantPhrases
}

func goldenCases() []goldenCase {
	return []goldenCase{
		// ── Gruppo 1 — facilissimo, deve fare 100% ─────────────────────
		{
			id: "T01", text: "Floyd Mayweather became one of the most recognizable boxers in the world.",
			wantRequired: true, wantQueries: []string{"Floyd Mayweather"},
			wantEntities: []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
			forbidQueries: []string{"boxing gloves", "Pacquiao", "Mayweather Boxing Club"},
		},
		{
			id: "T02", text: "Manny Pacquiao became a national icon in the Philippines.",
			wantRequired: true, wantQueries: []string{"Manny Pacquiao", "Philippines"},
			wantEntities: []wantEntity{
				{"PERSON", "Manny Pacquiao", "person:manny-pacquiao"},
				{"GPE", "Philippines", "gpe:philippines"},
			},
			// The two entities must stay distinct — never a single
			// "Manny Pacquiao Philippines national icon" blob.
			forbidQueries: []string{"Manny Pacquiao Philippines national icon", "national icon"},
		},
		{
			id: "T03", text: "Mike Tyson dominated the heavyweight division during the late 1980s.",
			wantRequired: true, wantQueries: []string{"Mike Tyson boxer"},
			wantEntities: []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
			wantVisual:   []wantEntity{{"DATE", "late 1980s", ""}},
			forbidQueries: []string{"heavyweight division", "late 1980s"},
		},
		{
			id: "T04", text: "Muhammad Ali became one of the most famous athletes in history.",
			wantRequired: true, wantQueries: []string{"Muhammad Ali"},
			wantEntities: []wantEntity{{"PERSON", "Muhammad Ali", "person:muhammad-ali"}},
		},
		{
			id: "T05", text: "Oleksandr Usyk won major heavyweight fights in Saudi Arabia.",
			wantRequired: true, wantQueries: []string{"Oleksandr Usyk", "Saudi Arabia"},
			wantEntities: []wantEntity{
				{"PERSON", "Oleksandr Usyk", "person:oleksandr-usyk"},
				{"GPE", "Saudi Arabia", "gpe:saudi-arabia"},
			},
		},
		{
			id: "T06", text: "Tyson Fury built his reputation through heavyweight boxing.",
			wantRequired: true, wantQueries: []string{"Tyson Fury boxer"},
			wantEntities:    []wantEntity{{"PERSON", "Tyson Fury", "person:tyson-fury"}},
			forbidEntities:  []wantEntity{{"PERSON", "Mike Tyson", ""}},
			forbidQueries:   []string{"Mike Tyson"},
		},

		// ── Gruppo 2 — due persone nella stessa frase ───────────────────
		{
			id: "T07", text: "Floyd Mayweather defeated Manny Pacquiao in one of boxing's biggest fights.",
			wantRequired: true, wantQueries: []string{"Floyd Mayweather", "Manny Pacquiao", "Floyd Mayweather Manny Pacquiao fight"},
			wantEntities: []wantEntity{
				{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"},
				{"PERSON", "Manny Pacquiao", "person:manny-pacquiao"},
			},
			// Two distinct entities, never one "defeated" blob.
			forbidQueries: []string{"Floyd Mayweather defeated Manny Pacquiao"},
		},
		{
			id: "T08", text: "Oleksandr Usyk faced Tyson Fury in a historic heavyweight showdown.",
			wantRequired: true, wantQueries: []string{"Oleksandr Usyk", "Tyson Fury", "Oleksandr Usyk Tyson Fury fight"},
			wantEntities: []wantEntity{
				{"PERSON", "Oleksandr Usyk", "person:oleksandr-usyk"},
				{"PERSON", "Tyson Fury", "person:tyson-fury"},
			},
			wantVisual: []wantEntity{{"EVENT", "historic heavyweight showdown", ""}},
		},
		{
			id: "T09", text: "Mike Tyson often receives comparisons with Muhammad Ali.",
			wantRequired: true, wantQueries: []string{"Mike Tyson", "Muhammad Ali"},
			wantEntities: []wantEntity{
				{"PERSON", "Mike Tyson", "person:mike-tyson"},
				{"PERSON", "Muhammad Ali", "person:muhammad-ali"},
			},
			// Both persons must be searchable — never five Tyson images and
			// zero Ali images.
			forbidQueries: []string{"comparisons"},
		},

		// ── Gruppo 3 — persona + soldi ──────────────────────────────────
		{
			id: "T10", text: "Floyd Mayweather reportedly earned more than $100 million from major fights.",
			wantRequired: true, wantQueries: []string{"Floyd Mayweather"},
			wantEntities: []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
			wantVisual:   []wantEntity{{"MONEY", "more than $100 million", ""}},
			wantPhrases:  []string{"earned more than $100 million"},
			// Money goes to the visual system (animated money graphic), NOT
			// to a stock-image search.
			forbidQueries: []string{"$100", "million"},
		},
		{
			id: "T11", text: "Manny Pacquiao earned hundreds of millions of dollars throughout his boxing career.",
			wantRequired: true, wantQueries: []string{"Manny Pacquiao"},
			wantEntities: []wantEntity{{"PERSON", "Manny Pacquiao", "person:manny-pacquiao"}},
			wantVisual:   []wantEntity{{"MONEY", "hundreds of millions of dollars", ""}},
			wantPhrases:  []string{"earned hundreds of millions of dollars"},
			forbidQueries: []string{"hundreds", "millions", "dollars", "boxing career"},
		},

		// ── Gruppo 4 — luoghi ───────────────────────────────────────────
		{
			id: "T12", text: "The Eiffel Tower remains one of the most recognizable landmarks in Paris.",
			wantRequired: true, wantQueries: []string{"Eiffel Tower Paris"},
			wantEntities: []wantEntity{
				{"LANDMARK", "Eiffel Tower", "landmark:eiffel-tower"},
				{"GPE", "Paris", "gpe:paris"},
			},
		},
		{
			id: "T13", text: "Buckingham Palace is located in London.",
			wantRequired: true, wantQueries: []string{"Buckingham Palace London"},
			wantEntities: []wantEntity{
				{"LANDMARK", "Buckingham Palace", "landmark:buckingham-palace"},
				{"GPE", "London", "gpe:london"},
			},
		},
		{
			id: "T14", text: "Times Square attracts millions of visitors to New York City.",
			wantRequired: true, wantQueries: []string{"Times Square New York City"},
			wantEntities: []wantEntity{
				{"LOCATION", "Times Square", "location:times-square"},
				{"GPE", "New York City", "gpe:new-york-city"},
			},
		},

		// ── Gruppo 5 — aziende/prodotti ─────────────────────────────────
		{
			id: "T15", text: "Apple introduced the Vision Pro as a new spatial computing device.",
			wantRequired: true, wantQueries: []string{"Apple Vision Pro"},
			wantEntities: []wantEntity{
				{"ORG", "Apple", "org:apple"},
				{"PRODUCT", "Apple Vision Pro", "product:apple-vision-pro"},
			},
			forbidQueries: []string{"apple fruit"},
		},
		{
			id: "T16", text: "Tesla's Cybertruck has one of the most unusual designs in the automotive industry.",
			wantRequired: true, wantQueries: []string{"Tesla Cybertruck"},
			wantEntities: []wantEntity{
				{"ORG", "Tesla", "org:tesla"},
				{"PRODUCT", "Tesla Cybertruck", "product:tesla-cybertruck"},
			},
			// Never the bare brand ("Tesla" alone returns Elon Musk/logo/Model 3).
			forbidQueries: []string{"Tesla model", "Elon Musk"},
		},
		{
			id: "T17", text: "SpaceX developed Starship for its next generation of space missions.",
			wantRequired: true, wantQueries: []string{"SpaceX Starship"},
			wantEntities: []wantEntity{
				{"ORG", "SpaceX", "org:spacex"},
				{"PRODUCT", "SpaceX Starship", "product:spacex-starship"},
			},
		},

		// ── Gruppo 6 — AMBIGUITÀ ────────────────────────────────────────
		{
			id: "T18", text: "Michael Jordan became a basketball legend with the Chicago Bulls.",
			wantRequired: true, wantQueries: []string{"Michael Jordan basketball", "Chicago Bulls"},
			wantEntities: []wantEntity{
				{"PERSON", "Michael Jordan", "person:michael-jordan"},
				{"ORG", "Chicago Bulls", "org:chicago-bulls"},
			},
			wantContexts:   []wantEntity{{"CONTEXT", "basketball", ""}},
			forbidEntities: []wantEntity{{"PERSON", "Michael B. Jordan", ""}},
			forbidQueries:  []string{"Michael B Jordan", "actor"},
		},
		{
			id: "T19", text: "Michael B. Jordan starred in several major Hollywood films.",
			wantRequired: true, wantQueries: []string{"Michael B Jordan actor"},
			wantEntities:   []wantEntity{{"PERSON", "Michael B. Jordan", "person:michael-b-jordan"}},
			wantContexts:   []wantEntity{{"CONTEXT", "actor", ""}},
			forbidEntities: []wantEntity{{"PERSON", "Michael Jordan", ""}},
			forbidQueries:  []string{"Michael Jordan", "NBA", "basketball"},
		},
		{
			id: "T20", text: "Apple reported strong demand for its latest devices.",
			wantRequired: true, wantQueries: []string{"Apple company"},
			wantEntities:   []wantEntity{{"ORG", "Apple", "org:apple"}},
			forbidEntities: []wantEntity{{"OBJECT", "apple fruit", ""}},
			forbidQueries:  []string{"apple fruit"},
		},
		{
			id: "T21", text: "The farmer picked a red apple from the tree.",
			wantRequired: true, wantQueries: []string{"red apple fruit"},
			wantEntities:   []wantEntity{{"OBJECT", "apple fruit", "object:apple-fruit"}},
			forbidEntities: []wantEntity{{"ORG", "Apple", ""}},
			forbidQueries:  []string{"Apple company", "Apple Inc"},
		},
		{
			id: "T22", text: "A jaguar moved silently through the Amazon rainforest.",
			wantRequired: true, wantQueries: []string{"jaguar animal Amazon rainforest"},
			wantEntities: []wantEntity{
				{"ANIMAL", "jaguar", "animal:jaguar"},
				{"LOCATION", "Amazon rainforest", "location:amazon-rainforest"},
			},
			forbidEntities: []wantEntity{{"ORG", "Jaguar", ""}},
			forbidQueries:  []string{"Jaguar car"},
		},
		{
			id: "T23", text: "Jaguar unveiled a new luxury vehicle.",
			wantRequired: true, wantQueries: []string{"Jaguar car"},
			wantEntities:   []wantEntity{{"ORG", "Jaguar", "org:jaguar"}},
			forbidEntities: []wantEntity{{"ANIMAL", "jaguar", ""}},
			forbidQueries:  []string{"jaguar animal"},
		},

		// ── Gruppo 7 — non deve cercare tutto ───────────────────────────
		{
			id: "T24", text: "Success often requires patience, discipline and consistency.",
			wantRequired: false,
			wantEntities: []wantEntity{},
		},
		{
			id: "T25", text: "The situation became increasingly complicated over time.",
			wantRequired: false,
			wantEntities: []wantEntity{},
		},
		{
			id: "T26", text: "His fortune changed dramatically over the following decade.",
			wantRequired: false, // no antecedent available → no canonical person
			wantEntities: []wantEntity{},
			forbidEntities: []wantEntity{{"PERSON", "Floyd Mayweather", ""}},
		},

		// ── Gruppo 9 — negazione ────────────────────────────────────────
		{
			id: "T27", text: "The fighter in this story is Tyson Fury, not Mike Tyson.",
			wantRequired: true, wantQueries: []string{"Tyson Fury boxer"},
			wantEntities: []wantEntity{{"PERSON", "Tyson Fury", "person:tyson-fury"}},
			wantNegated:  []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
			// The negated person must never drive an image.
			forbidQueries: []string{"Mike Tyson"},
		},

		// ── Gruppo 10 — entità dentro una frase lunga ───────────────────
		{
			id: "T28", text: "After earning huge purses from fights against Manny Pacquiao and other stars, Floyd Mayweather invested heavily in real estate and expanded the Mayweather Promotions brand.",
			wantRequired: true,
			wantQueries:  []string{"Floyd Mayweather", "Manny Pacquiao", "Mayweather Promotions", "real estate"},
			wantEntities: []wantEntity{
				{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"},
				{"PERSON", "Manny Pacquiao", "person:manny-pacquiao"},
				{"ORG", "Mayweather Promotions", "org:mayweather-promotions"},
				{"CATEGORY", "real estate", "category:real-estate"},
			},
			wantVisual:  []wantEntity{{"MONEY", "huge purses", ""}},
			wantPhrases: []string{"earning huge purses"},
		},

		// ── Coreference scene (Gruppo 8) ────────────────────────────────
		{
			id: "SCENE", text: "He later invested part of his fortune in several businesses.",
			prior:        []string{"Floyd Mayweather"},
			wantRequired: true, wantQueries: []string{"Floyd Mayweather"},
			wantEntities: []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
		},
	}
}

// goldenCasesIT is the Italian port of the battery. The INPUT is Italian;
// the expected queries, entities and canonical ids are IDENTICAL to the
// English battery because canonicalization is language-invariant: "Torre
// Eiffel" canonicalizes to landmark:eiffel-tower and queries "Eiffel Tower
// Paris", "Arabia Saudita" to gpe:saudi-arabia, "ha sconfitto" triggers the
// same "N1 N2 fight" event query. Only the verbatim value entities (MONEY /
// DATE / EVENT surfaces), the important phrases and the Italian forbidden
// surfaces differ.
func goldenCasesIT() []goldenCase {
	return []goldenCase{
		// ── Gruppo 1 — facilissimo, deve fare 100% ─────────────────────
		{
			id: "T01", text: "Floyd Mayweather è diventato uno dei pugili più riconoscibili al mondo.",
			wantRequired: true, wantQueries: []string{"Floyd Mayweather"},
			wantEntities: []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
			forbidQueries: []string{"guanti", "Pacquiao", "Mayweather Boxing Club", "pugile"},
		},
		{
			id: "T02", text: "Manny Pacquiao è diventato un'icona nazionale nelle Filippine.",
			wantRequired: true, wantQueries: []string{"Manny Pacquiao", "Philippines"},
			wantEntities: []wantEntity{
				{"PERSON", "Manny Pacquiao", "person:manny-pacquiao"},
				{"GPE", "Philippines", "gpe:philippines"},
			},
			forbidQueries: []string{"icona nazionale", "Manny Pacquiao Filippine"},
		},
		{
			id: "T03", text: "Mike Tyson ha dominato la divisione dei pesi massimi alla fine degli anni '80.",
			wantRequired: true, wantQueries: []string{"Mike Tyson boxer"},
			wantEntities: []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
			wantVisual:   []wantEntity{{"DATE", "fine degli anni '80", ""}},
			forbidQueries: []string{"pesi massimi", "anni '80"},
		},
		{
			id: "T04", text: "Muhammad Ali è diventato uno degli atleti più famosi della storia.",
			wantRequired: true, wantQueries: []string{"Muhammad Ali"},
			wantEntities: []wantEntity{{"PERSON", "Muhammad Ali", "person:muhammad-ali"}},
		},
		{
			id: "T05", text: "Oleksandr Usyk ha vinto importanti combattimenti dei pesi massimi in Arabia Saudita.",
			wantRequired: true, wantQueries: []string{"Oleksandr Usyk", "Saudi Arabia"},
			wantEntities: []wantEntity{
				{"PERSON", "Oleksandr Usyk", "person:oleksandr-usyk"},
				{"GPE", "Saudi Arabia", "gpe:saudi-arabia"},
			},
		},
		{
			id: "T06", text: "Tyson Fury ha costruito la sua reputazione attraverso la boxe dei pesi massimi.",
			wantRequired: true, wantQueries: []string{"Tyson Fury boxer"},
			wantEntities:    []wantEntity{{"PERSON", "Tyson Fury", "person:tyson-fury"}},
			forbidEntities:  []wantEntity{{"PERSON", "Mike Tyson", ""}},
			forbidQueries:   []string{"Mike Tyson"},
		},

		// ── Gruppo 2 — due persone nella stessa frase ───────────────────
		{
			id: "T07", text: "Floyd Mayweather ha sconfitto Manny Pacquiao in uno dei combattimenti più grandi della boxe.",
			wantRequired: true, wantQueries: []string{"Floyd Mayweather", "Manny Pacquiao", "Floyd Mayweather Manny Pacquiao fight"},
			wantEntities: []wantEntity{
				{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"},
				{"PERSON", "Manny Pacquiao", "person:manny-pacquiao"},
			},
			forbidQueries: []string{"Floyd Mayweather ha sconfitto Manny Pacquiao"},
		},
		{
			id: "T08", text: "Oleksandr Usyk ha affrontato Tyson Fury in uno storico scontro dei pesi massimi.",
			wantRequired: true, wantQueries: []string{"Oleksandr Usyk", "Tyson Fury", "Oleksandr Usyk Tyson Fury fight"},
			wantEntities: []wantEntity{
				{"PERSON", "Oleksandr Usyk", "person:oleksandr-usyk"},
				{"PERSON", "Tyson Fury", "person:tyson-fury"},
			},
			wantVisual: []wantEntity{{"EVENT", "storico scontro dei pesi massimi", ""}},
		},
		{
			id: "T09", text: "Mike Tyson riceve spesso paragoni con Muhammad Ali.",
			wantRequired: true, wantQueries: []string{"Mike Tyson", "Muhammad Ali"},
			wantEntities: []wantEntity{
				{"PERSON", "Mike Tyson", "person:mike-tyson"},
				{"PERSON", "Muhammad Ali", "person:muhammad-ali"},
			},
			forbidQueries: []string{"paragoni"},
		},

		// ── Gruppo 3 — persona + soldi ──────────────────────────────────
		{
			id: "T10", text: "Floyd Mayweather avrebbe guadagnato più di 100 milioni di dollari dai grandi combattimenti.",
			wantRequired: true, wantQueries: []string{"Floyd Mayweather"},
			wantEntities: []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
			wantVisual:   []wantEntity{{"MONEY", "più di 100 milioni di dollari", ""}},
			wantPhrases:  []string{"guadagnato più di 100 milioni di dollari"},
			forbidQueries: []string{"milioni", "dollari"},
		},
		{
			id: "T11", text: "Manny Pacquiao ha guadagnato centinaia di milioni di dollari durante la sua carriera di pugile.",
			wantRequired: true, wantQueries: []string{"Manny Pacquiao"},
			wantEntities: []wantEntity{{"PERSON", "Manny Pacquiao", "person:manny-pacquiao"}},
			wantVisual:   []wantEntity{{"MONEY", "centinaia di milioni di dollari", ""}},
			wantPhrases:  []string{"guadagnato centinaia di milioni di dollari"},
			forbidQueries: []string{"centinaia", "milioni", "dollari", "carriera"},
		},

		// ── Gruppo 4 — luoghi ───────────────────────────────────────────
		{
			id: "T12", text: "La Torre Eiffel rimane uno dei monumenti più riconoscibili di Parigi.",
			wantRequired: true, wantQueries: []string{"Eiffel Tower Paris"},
			wantEntities: []wantEntity{
				{"LANDMARK", "Eiffel Tower", "landmark:eiffel-tower"},
				{"GPE", "Paris", "gpe:paris"},
			},
		},
		{
			id: "T13", text: "Buckingham Palace si trova a Londra.",
			wantRequired: true, wantQueries: []string{"Buckingham Palace London"},
			wantEntities: []wantEntity{
				{"LANDMARK", "Buckingham Palace", "landmark:buckingham-palace"},
				{"GPE", "London", "gpe:london"},
			},
		},
		{
			id: "T14", text: "Times Square attira milioni di visitatori a New York.",
			wantRequired: true, wantQueries: []string{"Times Square New York City"},
			wantEntities: []wantEntity{
				{"LOCATION", "Times Square", "location:times-square"},
				{"GPE", "New York City", "gpe:new-york-city"},
			},
		},

		// ── Gruppo 5 — aziende/prodotti ─────────────────────────────────
		{
			id: "T15", text: "Apple ha introdotto il Vision Pro come nuovo dispositivo di elaborazione spaziale.",
			wantRequired: true, wantQueries: []string{"Apple Vision Pro"},
			wantEntities: []wantEntity{
				{"ORG", "Apple", "org:apple"},
				{"PRODUCT", "Apple Vision Pro", "product:apple-vision-pro"},
			},
			forbidQueries: []string{"apple fruit", "mela"},
		},
		{
			id: "T16", text: "Tesla ha presentato il Cybertruck con uno dei design più insoliti del settore automobilistico.",
			wantRequired: true, wantQueries: []string{"Tesla Cybertruck"},
			wantEntities: []wantEntity{
				{"ORG", "Tesla", "org:tesla"},
				{"PRODUCT", "Tesla Cybertruck", "product:tesla-cybertruck"},
			},
			forbidQueries: []string{"Elon Musk", "Tesla model"},
		},
		{
			id: "T17", text: "SpaceX ha sviluppato Starship per la sua prossima generazione di missioni spaziali.",
			wantRequired: true, wantQueries: []string{"SpaceX Starship"},
			wantEntities: []wantEntity{
				{"ORG", "SpaceX", "org:spacex"},
				{"PRODUCT", "SpaceX Starship", "product:spacex-starship"},
			},
		},

		// ── Gruppo 6 — AMBIGUITÀ ────────────────────────────────────────
		{
			id: "T18", text: "Michael Jordan è diventato una leggenda del basket con i Chicago Bulls.",
			wantRequired: true, wantQueries: []string{"Michael Jordan basketball", "Chicago Bulls"},
			wantEntities: []wantEntity{
				{"PERSON", "Michael Jordan", "person:michael-jordan"},
				{"ORG", "Chicago Bulls", "org:chicago-bulls"},
			},
			wantContexts:   []wantEntity{{"CONTEXT", "basketball", ""}},
			forbidEntities: []wantEntity{{"PERSON", "Michael B. Jordan", ""}},
			forbidQueries:  []string{"Michael B Jordan", "attore"},
		},
		{
			id: "T19", text: "Michael B. Jordan ha recitato in diversi film importanti di Hollywood.",
			wantRequired: true, wantQueries: []string{"Michael B Jordan actor"},
			wantEntities:   []wantEntity{{"PERSON", "Michael B. Jordan", "person:michael-b-jordan"}},
			wantContexts:   []wantEntity{{"CONTEXT", "actor", ""}},
			forbidEntities: []wantEntity{{"PERSON", "Michael Jordan", ""}},
			forbidQueries:  []string{"Michael Jordan", "NBA", "basketball"},
		},
		{
			id: "T20", text: "Apple ha riportato una forte domanda per i suoi ultimi dispositivi.",
			wantRequired: true, wantQueries: []string{"Apple company"},
			wantEntities:   []wantEntity{{"ORG", "Apple", "org:apple"}},
			forbidEntities: []wantEntity{{"OBJECT", "apple fruit", ""}},
			forbidQueries:  []string{"apple fruit", "mela"},
		},
		{
			id: "T21", text: "Il contadino ha raccolto una mela rossa dall'albero.",
			wantRequired: true, wantQueries: []string{"red apple fruit"},
			wantEntities:   []wantEntity{{"OBJECT", "apple fruit", "object:apple-fruit"}},
			forbidEntities: []wantEntity{{"ORG", "Apple", ""}},
			forbidQueries:  []string{"Apple company", "Apple Inc"},
		},
		{
			id: "T22", text: "Un giaguaro si muoveva silenziosamente attraverso la foresta amazzonica.",
			wantRequired: true, wantQueries: []string{"jaguar animal Amazon rainforest"},
			wantEntities: []wantEntity{
				{"ANIMAL", "jaguar", "animal:jaguar"},
				{"LOCATION", "Amazon rainforest", "location:amazon-rainforest"},
			},
			forbidEntities: []wantEntity{{"ORG", "Jaguar", ""}},
			forbidQueries:  []string{"Jaguar car"},
		},
		{
			id: "T23", text: "Jaguar ha svelato un nuovo veicolo di lusso.",
			wantRequired: true, wantQueries: []string{"Jaguar car"},
			wantEntities:   []wantEntity{{"ORG", "Jaguar", "org:jaguar"}},
			forbidEntities: []wantEntity{{"ANIMAL", "jaguar", ""}},
			forbidQueries:  []string{"jaguar animal", "giaguaro"},
		},

		// ── Gruppo 7 — non deve cercare tutto ───────────────────────────
		{
			id: "T24", text: "Il successo richiede spesso pazienza, disciplina e costanza.",
			wantRequired: false,
			wantEntities: []wantEntity{},
		},
		{
			id: "T25", text: "La situazione è diventata sempre più complicata nel tempo.",
			wantRequired: false,
			wantEntities: []wantEntity{},
		},
		{
			id: "T26", text: "La sua fortuna è cambiata drasticamente nel decennio successivo.",
			wantRequired: false, // no antecedent available → no canonical person
			wantEntities: []wantEntity{},
			forbidEntities: []wantEntity{{"PERSON", "Floyd Mayweather", ""}},
		},

		// ── Gruppo 9 — negazione ────────────────────────────────────────
		{
			id: "T27", text: "Il combattente in questa storia è Tyson Fury, non Mike Tyson.",
			wantRequired: true, wantQueries: []string{"Tyson Fury boxer"},
			wantEntities: []wantEntity{{"PERSON", "Tyson Fury", "person:tyson-fury"}},
			wantNegated:  []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
			forbidQueries: []string{"Mike Tyson"},
		},

		// ── Gruppo 10 — entità dentro una frase lunga ───────────────────
		{
			id: "T28", text: "Dopo aver guadagnato enormi borse da combattimento contro Manny Pacquiao e altri avversari, Floyd Mayweather ha investito pesantemente nel settore immobiliare e ha ampliato il marchio Mayweather Promotions.",
			wantRequired: true,
			wantQueries:  []string{"Floyd Mayweather", "Manny Pacquiao", "Mayweather Promotions", "real estate"},
			wantEntities: []wantEntity{
				{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"},
				{"PERSON", "Manny Pacquiao", "person:manny-pacquiao"},
				{"ORG", "Mayweather Promotions", "org:mayweather-promotions"},
				{"CATEGORY", "real estate", "category:real-estate"},
			},
			wantVisual:  []wantEntity{{"MONEY", "enormi borse da combattimento", ""}},
			wantPhrases: []string{"guadagnato enormi borse da combattimento"},
		},

		// ── Coreference scene (Gruppo 8) — pro-drop italiano ────────────
		{
			// "In seguito ha investito …" = "He later invested …": the
			// subject is DROPPED and must ground on the prior person.
			id: "SCENE", text: "In seguito ha investito parte della sua fortuna in diverse attività.",
			prior:        []string{"Floyd Mayweather"},
			wantRequired: true, wantQueries: []string{"Floyd Mayweather"},
			wantEntities: []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
		},
	}
}

// caseMetrics accumulates one battery row.
type caseMetrics struct {
	id               string
	expected         int
	found            int
	missing          []string
	falsePositives   []string
	canonicalChecked int
	canonicalOK      int
	canonicalMiss    []string
	queries          int
	queriesOK        bool
	requiredOK       bool
	noImageOK        bool
	wrongIdentity    int
	negatedSeen      int
}

func (m *caseMetrics) pass() bool {
	return m.requiredOK && m.queriesOK && len(m.missing) == 0 && len(m.falsePositives) == 0 &&
		len(m.canonicalMiss) == 0 && m.wrongIdentity == 0 && m.negatedSeen == 0
}

// runGoldenCase runs one sentence through the real resolver and asserts every
// expected surface, returning the metrics row. The language selects the
// battery variant ("en" or "it"); the expected queries/entities/canonical
// ids are identical across languages (canonicalization is language-
// invariant).
func runGoldenCase(t *testing.T, resolver *Resolver, gc goldenCase, lang string) caseMetrics {
	t.Helper()
	metrics := caseMetrics{id: lang + ":" + gc.id, expected: len(gc.wantEntities) + len(gc.wantVisual) + len(gc.wantContexts) + len(gc.wantNegated)}

	dec := resolver.Resolve(context.Background(), Request{Text: gc.text, Language: lang, PriorPersons: gc.prior})

	// 1. image_search_required decision.
	metrics.requiredOK = dec.Required == gc.wantRequired
	if !metrics.requiredOK {
		t.Errorf("[%s] image_search_required = %v, want %v (reason %q)", gc.id, dec.Required, gc.wantRequired, dec.NoImageReason)
	}

	// 2. Query list (exact, ordered).
	metrics.queries = len(dec.Queries)
	metrics.queriesOK = strings.Join(dec.Queries, "|") == strings.Join(gc.wantQueries, "|")
	if !metrics.queriesOK {
		t.Errorf("[%s] queries = %v, want %v", gc.id, dec.Queries, gc.wantQueries)
	}
	if !gc.wantRequired && len(dec.Queries) > 0 {
		t.Errorf("[%s] abstract sentence must not produce queries: %v", gc.id, dec.Queries)
	}

	// 3. Forbidden queries (wrong identity / negated person leaking).
	for _, forbidden := range gc.forbidQueries {
		for _, q := range dec.Queries {
			if strings.Contains(strings.ToLower(q), strings.ToLower(forbidden)) {
				metrics.wrongIdentity++
				t.Errorf("[%s] forbidden query surfaced: %q (contains %q)", gc.id, q, forbidden)
			}
		}
	}

	// 4. Forbidden entities.
	for _, forbidden := range gc.forbidEntities {
		for _, e := range detectedEntities(dec) {
			if e.Type == forbidden.typ && strings.EqualFold(e.Text, forbidden.text) {
				metrics.wrongIdentity++
				t.Errorf("[%s] forbidden entity surfaced: %s %q", gc.id, e.Type, e.Text)
			}
		}
	}

	// 5. Expected entities (typed + canonical).
	expected := append([]wantEntity(nil), gc.wantEntities...)
	expected = append(expected, gc.wantVisual...)
	expected = append(expected, gc.wantContexts...)
	expected = append(expected, gc.wantNegated...)
	detected := detectedEntities(dec)
	expectedSet := make(map[string]wantEntity, len(expected))
	for _, want := range expected {
		expectedSet[entityKey(want.typ, want.text)] = want
	}
	detectedSet := make(map[string]bool, len(detected))
	for _, e := range detected {
		detectedSet[entityKey(e.Type, e.Text)] = true
	}
	for _, want := range expected {
		if !detectedSet[entityKey(want.typ, want.text)] {
			metrics.missing = append(metrics.missing, fmt.Sprintf("%s %q", want.typ, want.text))
			t.Errorf("[%s] missing entity: %s %q", gc.id, want.typ, want.text)
			continue
		}
		metrics.found++
		if want.canonical == "" {
			continue
		}
		metrics.canonicalChecked++
		got := findDetected(detected, want.typ, want.text)
		if got.CanonicalID == want.canonical {
			metrics.canonicalOK++
		} else {
			metrics.canonicalMiss = append(metrics.canonicalMiss, fmt.Sprintf("%s %q → %q want %q", want.typ, want.text, got.CanonicalID, want.canonical))
			t.Errorf("[%s] canonical id: %s %q = %q, want %q", gc.id, want.typ, want.text, got.CanonicalID, want.canonical)
		}
	}
	for _, e := range detected {
		if _, ok := expectedSet[entityKey(e.Type, e.Text)]; !ok {
			metrics.falsePositives = append(metrics.falsePositives, fmt.Sprintf("%s %q", e.Type, e.Text))
			t.Errorf("[%s] false positive entity: %s %q", gc.id, e.Type, e.Text)
		}
	}

	// 6. Negated list.
	for _, want := range gc.wantNegated {
		found := false
		for _, e := range dec.Negated {
			if e.Type == want.typ && strings.EqualFold(e.Text, want.text) {
				found = true
				if want.canonical != "" && e.CanonicalID != want.canonical {
					metrics.canonicalMiss = append(metrics.canonicalMiss, fmt.Sprintf("negated %s %q", want.typ, want.text))
				}
			}
		}
		if !found {
			t.Errorf("[%s] negated entity missing: %s %q (negated=%+v)", gc.id, want.typ, want.text, dec.Negated)
		}
	}
	// A negated person must never be selected as primary or appear in queries.
	for _, e := range dec.Negated {
		for _, q := range dec.Queries {
			if strings.Contains(strings.ToLower(q), strings.ToLower(e.Text)) {
				metrics.negatedSeen++
				t.Errorf("[%s] negated person %q leaked into query %q", gc.id, e.Text, q)
			}
		}
		if dec.Primary != nil && dec.Primary.CanonicalID == e.CanonicalID && e.CanonicalID != "" {
			metrics.negatedSeen++
			t.Errorf("[%s] negated person %q selected as primary", gc.id, e.Text)
		}
	}

	// 7. Important phrases.
	for _, want := range gc.wantPhrases {
		found := false
		for _, phrase := range dec.ImportantPhrases {
			if strings.Contains(strings.ToLower(phrase), strings.ToLower(want)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("[%s] important phrase %q missing (phrases=%v)", gc.id, want, dec.ImportantPhrases)
		}
	}

	// 8. No-image metric (abstract sentences must decide Required=false and
	// emit zero queries).
	if !gc.wantRequired {
		metrics.noImageOK = !dec.Required && len(dec.Queries) == 0
	}

	return metrics
}

// detectedEntities flattens every typed surface of the decision.
func detectedEntities(dec ImageSearchDecision) []ResolvedEntity {
	out := make([]ResolvedEntity, 0, len(dec.Entities)+len(dec.Contexts)+len(dec.Visual)+len(dec.Negated))
	out = append(out, dec.Entities...)
	out = append(out, dec.Contexts...)
	out = append(out, dec.Visual...)
	out = append(out, dec.Negated...)
	return out
}

func entityKey(typ, text string) string {
	return typ + "\x00" + strings.ToLower(strings.TrimSpace(text))
}

func findDetected(detected []ResolvedEntity, typ, text string) ResolvedEntity {
	for _, e := range detected {
		if e.Type == typ && strings.EqualFold(e.Text, text) {
			return e
		}
	}
	return ResolvedEntity{}
}

// TestGoldenBattery_ImageSearch runs the English battery.
func TestGoldenBattery_ImageSearch(t *testing.T) {
	runBattery(t, "en", goldenCases())
}

// TestGoldenBattery_ImageSearch_IT runs the Italian battery (same canonical
// queries/entities/ids, Italian input).
func TestGoldenBattery_ImageSearch_IT(t *testing.T) {
	runBattery(t, "it", goldenCasesIT())
}

// runBattery runs the whole battery for one language, asserts the
// certification floor, and prints the per-sentence metric table.
func runBattery(t *testing.T, lang string, cases []goldenCase) {
	resolver := NewResolver(localnlp.NewExtractor())

	var rows []caseMetrics
	var noImageRows []caseMetrics
	for _, gc := range cases {
		t.Run(lang+":"+gc.id, func(t *testing.T) {
			row := runGoldenCase(t, resolver, gc, lang)
			rows = append(rows, row)
			if !gc.wantRequired {
				noImageRows = append(noImageRows, row)
			}
		})
	}

	// ── Aggregate metrics ────────────────────────────────────────────
	var totalExpected, totalFound, totalFP, canonicalChecked, canonicalOK int
	for _, row := range rows {
		totalExpected += row.expected
		totalFound += row.found
		totalFP += len(row.falsePositives)
		canonicalChecked += row.canonicalChecked
		canonicalOK += row.canonicalOK
	}

	noImageTotal := len(noImageRows)
	noImageOK := 0
	for _, row := range noImageRows {
		if row.noImageOK {
			noImageOK++
		}
	}

	recall := float64(totalFound) / float64(totalExpected)
	precision := float64(totalFound) / float64(totalFound+totalFP)
	canonicalAccuracy := float64(canonicalOK) / float64(canonicalChecked)

	var wrongIdentity, negatedSeen int
	for _, row := range rows {
		wrongIdentity += row.wrongIdentity
		negatedSeen += row.negatedSeen
	}

	// ── Report ───────────────────────────────────────────────────────
	var report strings.Builder
	report.WriteString("\n===== GOLDEN BATTERY: IMAGE SEARCH [" + lang + "] =====\n")
	report.WriteString(fmt.Sprintf("%-10s %-10s %-8s %-8s %-8s\n", "ID", "required", "entities", "queries", "verdict"))
	for _, row := range rows {
		verdict := "PASS"
		if !row.pass() {
			verdict = "FAIL"
		}
		report.WriteString(fmt.Sprintf("%-10s %-10v %-8d %-8d %s\n", row.id, row.requiredOK, row.found, row.queries, verdict))
	}
	report.WriteString("\n--- metrics ---\n")
	report.WriteString(fmt.Sprintf("entity recall                 = %.4f (%d/%d)\n", recall, totalFound, totalExpected))
	report.WriteString(fmt.Sprintf("entity precision              = %.4f (%d/%d)\n", precision, totalFound, totalFound+totalFP))
	report.WriteString(fmt.Sprintf("canonicalization accuracy     = %.4f (%d/%d)\n", canonicalAccuracy, canonicalOK, canonicalChecked))
	report.WriteString(fmt.Sprintf("no-image decision accuracy    = %.4f (%d/%d)\n", float64(noImageOK)/float64(noImageTotal), noImageOK, noImageTotal))
	report.WriteString(fmt.Sprintf("wrong-identity selections     = %d\n", wrongIdentity))
	report.WriteString(fmt.Sprintf("negated-person selections     = %d\n", negatedSeen))
	report.WriteString("CERTIFICATION THRESHOLDS: recall>=0.95 precision>=0.95 canonical>=0.98 no-image=1.0 wrong=0 negated=0\n")
	t.Log(report.String())

	// ── Certification floor (the spec's initial targets) ─────────────
	require.GreaterOrEqual(t, recall, 0.95, "entity recall must meet the certification floor")
	require.GreaterOrEqual(t, precision, 0.95, "entity precision must meet the certification floor")
	require.GreaterOrEqual(t, canonicalAccuracy, 0.98, "canonicalization accuracy must meet the certification floor")
	require.Equal(t, noImageTotal, noImageOK, "every abstract sentence must decide no-image correctly (T24/T25/T26)")
	require.Zero(t, wrongIdentity, "wrong-identity selections must be zero (T06/T18/T19/T20/T21/T22/T23)")
	require.Zero(t, negatedSeen, "negated-person selections must be zero (T27)")
	for _, row := range rows {
		require.True(t, row.pass(), "battery case %s must pass: %+v", row.id, row)
	}
}

// TestGoldenBattery_FullParagraph certifies the complete English paragraph
// the spec highlights as the closest to real PipelineGen input, threading
// pronoun coreference across sentences.
func TestGoldenBattery_FullParagraph(t *testing.T) {
	assertParagraph(t, "en", []string{
		"Floyd Mayweather built one of boxing's most recognizable brands.",
		"After earning enormous purses from fights against Manny Pacquiao and other opponents, he expanded Mayweather Promotions and invested in real estate.",
		"His financial success turned him from a championship boxer into a global businessman.",
	})
}

// TestGoldenBattery_FullParagraph_IT certifies the same paragraph in
// Italian, where sentence 2 exercises the pro-drop subject ("ha ampliato …"
// = "he expanded …") and sentence 3 the article+possessive opener ("Il suo
// successo …" = "His success …").
func TestGoldenBattery_FullParagraph_IT(t *testing.T) {
	assertParagraph(t, "it", []string{
		"Floyd Mayweather ha costruito uno dei marchi più riconoscibili della boxe.",
		"Dopo aver guadagnato enormi borse da combattimento contro Manny Pacquiao e altri avversari, ha ampliato Mayweather Promotions e ha investito nel settore immobiliare.",
		"Il suo successo finanziario lo ha trasformato da pugile campione in imprenditore globale.",
	})
}

// assertParagraph runs a three-sentence paragraph through the resolver with
// coreference threading and asserts the per-sentence surfaces plus the
// aggregate semantic extraction. The expected queries and canonical ids are
// language-invariant.
func assertParagraph(t *testing.T, lang string, paragraph []string) {
	resolver := NewResolver(localnlp.NewExtractor())

	var prior []string
	aggregate := map[string]bool{}
	for i, sentence := range paragraph {
		dec := resolver.Resolve(context.Background(), Request{Text: sentence, Language: lang, PriorPersons: prior})
		require.True(t, dec.Required, "sentence %d must require an image search", i+1)
		require.NotNil(t, dec.Primary, "sentence %d must have a primary entity", i+1)

		for _, e := range dec.Entities {
			aggregate[entityKey(e.Type, e.Text)] = true
		}

		switch i {
		case 0:
			require.Equal(t, "person:floyd-mayweather", dec.Primary.CanonicalID, "sentence 1 primary")
			require.Equal(t, []string{"Floyd Mayweather"}, dec.Queries, "sentence 1 queries")
		case 1:
			// "he" / the dropped subject must resolve to Floyd Mayweather
			// even though the name is not in the sentence.
			require.Equal(t, "person:floyd-mayweather", dec.Primary.CanonicalID, "sentence 2 primary must be the coreference-resolved Floyd Mayweather")
			require.Equal(t, []string{"Floyd Mayweather", "Manny Pacquiao", "Mayweather Promotions", "real estate"}, dec.Queries, "sentence 2 queries")
			require.Equal(t, "MONEY", dec.Visual[0].Type, "sentence 2 must carry the money visual (enormous purses / enormi borse)")
		case 2:
			require.Equal(t, "person:floyd-mayweather", dec.Primary.CanonicalID, "sentence 3 primary")
			require.Equal(t, []string{"Floyd Mayweather"}, dec.Queries, "sentence 3 queries")
		}

		// Thread the resolved primary forward as the coreference antecedent.
		if dec.Primary != nil {
			prior = append([]string{dec.Primary.Text}, prior...)
		}
	}

	// The paragraph's expected semantic extraction (per the spec):
	// Floyd Mayweather (PERSON), Manny Pacquiao (PERSON),
	// Mayweather Promotions (ORG), real estate (CATEGORY).
	for _, want := range []wantEntity{
		{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"},
		{"PERSON", "Manny Pacquiao", "person:manny-pacquiao"},
		{"ORG", "Mayweather Promotions", "org:mayweather-promotions"},
		{"CATEGORY", "real estate", "category:real-estate"},
	} {
		require.True(t, aggregate[entityKey(want.typ, want.text)], "paragraph must extract %s %q", want.typ, want.text)
	}
}
