package imagesearch

func goldenCasesITB() []goldenCase {
	return []goldenCase{
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
