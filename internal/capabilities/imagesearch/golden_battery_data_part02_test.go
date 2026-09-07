package imagesearch

func goldenCasesB() []goldenCase {
	return []goldenCase{
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
	return append(goldenCasesITA(), goldenCasesITB()...)
}
