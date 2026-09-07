package imagesearch

func goldenCases() []goldenCase {
	return append(goldenCasesA(), goldenCasesB()...)
}

func goldenCasesA() []goldenCase {
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
	}
}
