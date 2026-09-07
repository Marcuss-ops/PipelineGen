package imagesearch

func goldenCasesITA() []goldenCase {
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
	}
}
