package imagesearch

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	localnlp "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/nlp/local"
)

func goldenCases() []goldenCase {
	return []goldenCase{
		// ── Gruppo 1 — facilissimo, deve fare 100% ─────────────────────
		{
			id: "T01", text: "Floyd Mayweather became one of the most recognizable boxers in the world.",
			wantRequired: true, wantQueries: []string{"Floyd Mayweather"},
			wantEntities:  []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
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
			wantEntities:  []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
			wantVisual:    []wantEntity{{"DATE", "late 1980s", ""}},
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
			wantEntities:   []wantEntity{{"PERSON", "Tyson Fury", "person:tyson-fury"}},
			forbidEntities: []wantEntity{{"PERSON", "Mike Tyson", ""}},
			forbidQueries:  []string{"Mike Tyson"},
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
			wantEntities:  []wantEntity{{"PERSON", "Manny Pacquiao", "person:manny-pacquiao"}},
			wantVisual:    []wantEntity{{"MONEY", "hundreds of millions of dollars", ""}},
			wantPhrases:   []string{"earned hundreds of millions of dollars"},
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
			wantRequired:   false, // no antecedent available → no canonical person
			wantEntities:   []wantEntity{},
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

		// ── Gruppo 11 — negazione oltre "not X" ───────────────────────
		{
			id: "T29", text: "The fighter in this story is Tyson Fury, instead of Mike Tyson.",
			wantRequired: true, wantQueries: []string{"Tyson Fury boxer"},
			wantEntities:  []wantEntity{{"PERSON", "Tyson Fury", "person:tyson-fury"}},
			wantNegated:   []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
			forbidQueries: []string{"Mike Tyson"},
		},
		{
			id: "T30", text: "Rather than Mike Tyson, the story follows Floyd Mayweather.",
			wantRequired: true, wantQueries: []string{"Floyd Mayweather"},
			wantEntities:  []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
			wantNegated:   []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
			forbidQueries: []string{"Mike Tyson"},
		},
		{
			id: "T31", text: "Unlike Mike Tyson, Floyd Mayweather avoided the spotlight.",
			wantRequired: true, wantQueries: []string{"Floyd Mayweather"},
			wantEntities:  []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
			wantNegated:   []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
			forbidQueries: []string{"Mike Tyson"},
		},

		// ── Gruppo 12 — alias coreference (beyond pronouns) ────────────
		{
			id: "T32", text: "The fighter later invested part of his fortune in several businesses.",
			prior:        []string{"Tyson Fury"},
			wantRequired: true, wantQueries: []string{"Tyson Fury boxer"},
			wantEntities:   []wantEntity{{"PERSON", "Tyson Fury", "person:tyson-fury"}},
			forbidEntities: []wantEntity{{"PERSON", "Mike Tyson", ""}},
			forbidQueries:  []string{"Mike Tyson", "fighter"},
		}, {
			// "The fighter" must NOT ground on a non-boxer prior: without a
			// resolvable antecedent the alias never invents an identity.
			id: "T33", text: "The fighter later invested part of his fortune in several businesses.",
			prior:          []string{"Steve Jobs"},
			wantRequired:   false,
			wantEntities:   []wantEntity{},
			forbidEntities: []wantEntity{{"PERSON", "Tyson Fury", ""}, {"PERSON", "Mike Tyson", ""}, {"PERSON", "Steve Jobs", ""}},
		},

		// ── Gruppo 13 — coppie identità (Mercurio/Giordania/Turchia) ────
		{
			id: "T34", text: "Mercury is the smallest planet in our solar system.",
			wantRequired: true, wantQueries: []string{"Mercury planet"},
			wantEntities:   []wantEntity{{"OBJECT", "Mercury", "object:mercury"}},
			forbidEntities: []wantEntity{{"PERSON", "Freddie Mercury", ""}},
			forbidQueries:  []string{"Freddie Mercury", "singer"},
		},
		{
			id: "T35", text: "Freddie Mercury was the lead singer of the rock band Queen.",
			wantRequired: true, wantQueries: []string{"Freddie Mercury singer"},
			wantEntities:   []wantEntity{{"PERSON", "Freddie Mercury", "person:freddie-mercury"}},
			wantContexts:   []wantEntity{{"CONTEXT", "singer", ""}},
			forbidEntities: []wantEntity{{"OBJECT", "Mercury", ""}},
			forbidQueries:  []string{"Mercury planet"},
		},
		{
			id: "T36", text: "Jordan is a small country with a rich history.",
			wantRequired: true, wantQueries: []string{"Jordan"},
			wantEntities:   []wantEntity{{"GPE", "Jordan", "gpe:jordan"}},
			forbidEntities: []wantEntity{{"PERSON", "Michael Jordan", ""}},
			forbidQueries:  []string{"Michael Jordan"},
		},
		{
			// "Michael Jordan" must never spawn the country entity "Jordan":
			// the surface "Jordan" is a SUBSTRING of "Michael Jordan", so the
			// country entry may only match under its country context gates.
			id: "T37", text: "Michael Jordan is a basketball legend.",
			wantRequired: true, wantQueries: []string{"Michael Jordan basketball"},
			wantEntities:   []wantEntity{{"PERSON", "Michael Jordan", "person:michael-jordan"}},
			wantContexts:   []wantEntity{{"CONTEXT", "basketball", ""}},
			forbidEntities: []wantEntity{{"GPE", "Jordan", ""}},
			forbidQueries:  []string{"country"},
		},
		{
			id: "T38", text: "Turkey is a country with a long coastline.",
			wantRequired: true, wantQueries: []string{"Turkey"},
			wantEntities:   []wantEntity{{"GPE", "Turkey", "gpe:turkey"}},
			forbidEntities: []wantEntity{{"ANIMAL", "turkey", ""}},
			forbidQueries:  []string{"turkey bird"},
		},
		{
			id: "T39", text: "The turkey strutted across the barnyard at dawn.",
			wantRequired: true, wantQueries: []string{"turkey bird"},
			wantEntities:   []wantEntity{{"ANIMAL", "turkey", "animal:turkey"}},
			forbidEntities: []wantEntity{{"GPE", "Turkey", ""}},
			forbidQueries:  []string{"Turkey country", "Ankara"},
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
			wantEntities:  []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
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
			wantEntities:  []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
			wantVisual:    []wantEntity{{"DATE", "fine degli anni '80", ""}},
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
			wantEntities:   []wantEntity{{"PERSON", "Tyson Fury", "person:tyson-fury"}},
			forbidEntities: []wantEntity{{"PERSON", "Mike Tyson", ""}},
			forbidQueries:  []string{"Mike Tyson"},
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
			wantEntities:  []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
			wantVisual:    []wantEntity{{"MONEY", "più di 100 milioni di dollari", ""}},
			wantPhrases:   []string{"guadagnato più di 100 milioni di dollari"},
			forbidQueries: []string{"milioni", "dollari"},
		},
		{
			id: "T11", text: "Manny Pacquiao ha guadagnato centinaia di milioni di dollari durante la sua carriera di pugile.",
			wantRequired: true, wantQueries: []string{"Manny Pacquiao"},
			wantEntities:  []wantEntity{{"PERSON", "Manny Pacquiao", "person:manny-pacquiao"}},
			wantVisual:    []wantEntity{{"MONEY", "centinaia di milioni di dollari", ""}},
			wantPhrases:   []string{"guadagnato centinaia di milioni di dollari"},
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
			wantRequired:   false, // no antecedent available → no canonical person
			wantEntities:   []wantEntity{},
			forbidEntities: []wantEntity{{"PERSON", "Floyd Mayweather", ""}},
		},

		// ── Gruppo 9 — negazione ────────────────────────────────────────
		{
			id: "T27", text: "Il combattente in questa storia è Tyson Fury, non Mike Tyson.",
			wantRequired: true, wantQueries: []string{"Tyson Fury boxer"},
			wantEntities:  []wantEntity{{"PERSON", "Tyson Fury", "person:tyson-fury"}},
			wantNegated:   []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
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

		// ── Gruppo 11 — negazione oltre "non X" ────────────────────────
		{
			id: "T29", text: "Il combattente in questa storia è Tyson Fury, invece di Mike Tyson.",
			wantRequired: true, wantQueries: []string{"Tyson Fury boxer"},
			wantEntities:  []wantEntity{{"PERSON", "Tyson Fury", "person:tyson-fury"}},
			wantNegated:   []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
			forbidQueries: []string{"Mike Tyson"},
		},
		{
			id: "T30", text: "Piuttosto che Mike Tyson, la storia segue Floyd Mayweather.",
			wantRequired: true, wantQueries: []string{"Floyd Mayweather"},
			wantEntities:  []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
			wantNegated:   []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
			forbidQueries: []string{"Mike Tyson"},
		},
		{
			id: "T31", text: "A differenza di Mike Tyson, Floyd Mayweather ha evitato i riflettori.",
			wantRequired: true, wantQueries: []string{"Floyd Mayweather"},
			wantEntities:  []wantEntity{{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"}},
			wantNegated:   []wantEntity{{"PERSON", "Mike Tyson", "person:mike-tyson"}},
			forbidQueries: []string{"Mike Tyson"},
		},

		// ── Gruppo 12 — alias coreference (oltre i pronomi) ────────────
		{
			id: "T32", text: "Il pugile in seguito ha investito parte della sua fortuna in diverse attività.",
			prior:        []string{"Tyson Fury"},
			wantRequired: true, wantQueries: []string{"Tyson Fury boxer"},
			wantEntities:   []wantEntity{{"PERSON", "Tyson Fury", "person:tyson-fury"}},
			forbidEntities: []wantEntity{{"PERSON", "Mike Tyson", ""}},
			forbidQueries:  []string{"Mike Tyson", "pugile"},
		}, {
			// "Il pugile" must NOT ground on a non-boxer prior: without a
			// resolvable antecedent the alias never invents an identity.
			id: "T33", text: "Il pugile in seguito ha investito parte della sua fortuna in diverse attività.",
			prior:          []string{"Steve Jobs"},
			wantRequired:   false,
			wantEntities:   []wantEntity{},
			forbidEntities: []wantEntity{{"PERSON", "Tyson Fury", ""}, {"PERSON", "Mike Tyson", ""}, {"PERSON", "Steve Jobs", ""}},
		},

		// ── Gruppo 13 — coppie identità (Mercurio/Giordania/Turchia) ────
		{
			id: "T34", text: "Mercurio è il pianeta più piccolo del sistema solare.",
			wantRequired: true, wantQueries: []string{"Mercury planet"},
			wantEntities:   []wantEntity{{"OBJECT", "Mercury", "object:mercury"}},
			forbidEntities: []wantEntity{{"PERSON", "Freddie Mercury", ""}},
			forbidQueries:  []string{"Freddie Mercury", "cantante"},
		},
		{
			id: "T35", text: "Freddie Mercury era il cantante principale della rock band Queen.",
			wantRequired: true, wantQueries: []string{"Freddie Mercury singer"},
			wantEntities:   []wantEntity{{"PERSON", "Freddie Mercury", "person:freddie-mercury"}},
			wantContexts:   []wantEntity{{"CONTEXT", "singer", ""}},
			forbidEntities: []wantEntity{{"OBJECT", "Mercury", ""}},
			forbidQueries:  []string{"Mercury planet"},
		},
		{
			id: "T36", text: "La Giordania è un piccolo paese con una storia ricca.",
			wantRequired: true, wantQueries: []string{"Jordan"},
			wantEntities:   []wantEntity{{"GPE", "Jordan", "gpe:jordan"}},
			forbidEntities: []wantEntity{{"PERSON", "Michael Jordan", ""}},
			forbidQueries:  []string{"Michael Jordan"},
		},
		{
			// "Michael Jordan" (IT surface invariato) non deve mai generare
			// l'entità paese "Jordan": in italiano la superficie "Giordania"
			// non è un substring, ma il gate di contesto deve comunque tenere.
			id: "T37", text: "Michael Jordan è una leggenda del basket.",
			wantRequired: true, wantQueries: []string{"Michael Jordan basketball"},
			wantEntities:   []wantEntity{{"PERSON", "Michael Jordan", "person:michael-jordan"}},
			wantContexts:   []wantEntity{{"CONTEXT", "basketball", ""}},
			forbidEntities: []wantEntity{{"GPE", "Jordan", ""}},
			forbidQueries:  []string{"Giordania"},
		},
		{
			id: "T38", text: "La Turchia è un paese con una lunga costa.",
			wantRequired: true, wantQueries: []string{"Turkey"},
			wantEntities:   []wantEntity{{"GPE", "Turkey", "gpe:turkey"}},
			forbidEntities: []wantEntity{{"ANIMAL", "turkey", ""}},
			forbidQueries:  []string{"turkey bird"},
		},
		{
			id: "T39", text: "Il tacchino faceva la ruota nel cortile della fattoria all'alba.",
			wantRequired: true, wantQueries: []string{"turkey bird"},
			wantEntities:   []wantEntity{{"ANIMAL", "turkey", "animal:turkey"}},
			forbidEntities: []wantEntity{{"GPE", "Turkey", ""}},
			forbidQueries:  []string{"Turchia", "paese"},
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
