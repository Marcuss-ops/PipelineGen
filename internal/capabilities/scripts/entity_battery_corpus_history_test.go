// Package scriptgeneration — entity_battery_corpus_history_test.go holds the
// history, geopolitics and business thirds of the Goal 4 ground-truth corpus.
package scriptgeneration

func historyScripts() []batteryScript {
	return []batteryScript{
		{
			ID: "script-11", Category: "history", Topic: "Julius Caesar and Rome",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Julius Caesar changed Rome forever.",
					Raw: []batteryEntity{
						be("Julius Caesar", "PERSON", 0.99),
						be("Rome", "CITY", 0.92),
					},
				},
				{
					ID: "scene-1", Text: "Caesar rebuilt Rome and Julius Caesar wrote memoirs.",
					Raw: []batteryEntity{
						be("Caesar", "PERSON", 0.94),
						be("Rome", "GPE", 0.90),
						be("Roman Senate", "ORG", 0.70),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Julius Caesar", "PERSON", "scene-0"),
				bx("Rome", "GPE", "scene-0"),
				bx("Julius Caesar", "PERSON", "scene-1"),
				bx("Rome", "GPE", "scene-1"),
			},
			Rejected: []string{"Roman Senate"},
		},
		{
			ID: "script-12", Category: "history", Topic: "Cleopatra and Alexandria",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Cleopatra ruled Egypt from Alexandria.",
					Raw: []batteryEntity{
						be("Cleopatra", "PERSON", 0.98),
						be("Egypt", "COUNTRY", 0.93),
						be("Alexandria", "CITY", 0.90),
					},
				},
				{
					ID: "scene-1", Text: "Cleopatra defended Alexandria while Egypt wavered.",
					Raw: []batteryEntity{
						be("Cleopatra", "PERSON", 0.97),
						be("Alexandria", "CITY", 0.88),
						be("Egypt", "COUNTRY", 0.89),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Cleopatra", "PERSON", "scene-0"),
				bx("Egypt", "GPE", "scene-0"),
				bx("Alexandria", "GPE", "scene-0"),
				bx("Cleopatra", "PERSON", "scene-1"),
				bx("Alexandria", "GPE", "scene-1"),
				bx("Egypt", "GPE", "scene-1"),
			},
		},
	}
}

func geopoliticsScripts() []batteryScript {
	return []batteryScript{
		{
			ID: "script-13", Category: "geopolitics", Topic: "Guterres and the United Nations",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "António Guterres addressed the United Nations in New York City.",
					Raw: []batteryEntity{
						be("António Guterres", "PERSON", 0.96),
						be("United Nations", "ORG", 0.95),
						be("New York City", "CITY", 0.90),
					},
				},
				{
					ID: "scene-1", Text: "Guterres warned the United Nations while António Guterres spoke in New York City.",
					Raw: []batteryEntity{
						be("Guterres", "PERSON", 0.92),
						be("António Guterres", "PERSON", 0.96),
						be("United Nations", "ORG", 0.94),
						be("New York City", "CITY", 0.87),
					},
				},
			},
			Expected: []batteryExpected{
				bx("António Guterres", "PERSON", "scene-0"),
				bx("United Nations", "ORG", "scene-0"),
				bx("New York City", "GPE", "scene-0"),
				bx("António Guterres", "PERSON", "scene-1"),
				bx("United Nations", "ORG", "scene-1"),
				bx("New York City", "GPE", "scene-1"),
			},
		},
		{
			ID: "script-14", Category: "geopolitics", Topic: "The European Union in Brussels",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Ursula von der Leyen leads the European Union from Brussels.",
					Raw: []batteryEntity{
						be("Ursula von der Leyen", "PERSON", 0.97),
						be("European Union", "ORG", 0.94),
						be("Brussels", "CITY", 0.88),
					},
				},
				{
					ID: "scene-1", Text: "The European Union met in Brussels with Ursula von der Leyen.",
					Raw: []batteryEntity{
						be("European Union", "ORG", 0.93),
						be("Brussels", "CITY", 0.86),
						be("Ursula von der Leyen", "PERSON", 0.95),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Ursula von der Leyen", "PERSON", "scene-0"),
				bx("European Union", "ORG", "scene-0"),
				bx("Brussels", "GPE", "scene-0"),
				bx("European Union", "ORG", "scene-1"),
				bx("Brussels", "GPE", "scene-1"),
				bx("Ursula von der Leyen", "PERSON", "scene-1"),
			},
		},
	}
}

func businessScripts() []batteryScript {
	return []batteryScript{
		{
			ID: "script-15", Category: "business", Topic: "Jeff Bezos and Amazon in Seattle",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Jeff Bezos founded Amazon in Seattle.",
					Raw: []batteryEntity{
						be("Jeff Bezos", "PERSON", 0.98),
						be("Amazon", "ORG", 0.95),
						be("Seattle", "CITY", 0.88),
					},
				},
				{
					ID: "scene-1", Text: "Bezos left Amazon while Jeff Bezos focused on Seattle.",
					Raw: []batteryEntity{
						be("Bezos", "PERSON", 0.92),
						be("Jeff Bezos", "PERSON", 0.97),
						be("Amazon", "ORG", 0.94),
						be("Seattle", "CITY", 0.85),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Jeff Bezos", "PERSON", "scene-0"),
				bx("Amazon", "ORG", "scene-0"),
				bx("Seattle", "GPE", "scene-0"),
				bx("Jeff Bezos", "PERSON", "scene-1"),
				bx("Amazon", "ORG", "scene-1"),
				bx("Seattle", "GPE", "scene-1"),
			},
		},
		{
			ID: "script-16", Category: "business", Topic: "L'Oréal and Paris",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Françoise Bettencourt leads L'Oréal from Paris.",
					Raw: []batteryEntity{
						be("Françoise Bettencourt", "PERSON", 0.96),
						be("L'Oréal", "COMPANY", 0.95),
						be("Paris", "CITY", 0.90),
					},
				},
				{
					ID: "scene-1", Text: "Bettencourt expanded L'Oréal while Françoise Bettencourt visited Paris.",
					Raw: []batteryEntity{
						be("Bettencourt", "PERSON", 0.93),
						be("Françoise Bettencourt", "PERSON", 0.96),
						be("L'Oréal", "COMPANY", 0.94),
						be("Paris", "CITY", 0.88),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Françoise Bettencourt", "PERSON", "scene-0"),
				bx("L'Oréal", "ORG", "scene-0"),
				bx("Paris", "GPE", "scene-0"),
				bx("Françoise Bettencourt", "PERSON", "scene-1"),
				bx("L'Oréal", "ORG", "scene-1"),
				bx("Paris", "GPE", "scene-1"),
			},
		},
	}
}
