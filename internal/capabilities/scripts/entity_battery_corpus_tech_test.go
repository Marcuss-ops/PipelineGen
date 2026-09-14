// Package scriptgeneration — entity_battery_corpus_tech_test.go holds the
// automotive and technology halves of the Goal 4 ground-truth corpus.
package scriptgeneration

func automotiveScripts() []batteryScript {
	return []batteryScript{
		{
			ID: "script-07", Category: "automotive", Topic: "Elon Musk and the Tesla factory",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Elon Musk built the Tesla factory near Berlin.",
					Raw: []batteryEntity{
						be("Elon Musk", "PERSON", 0.98),
						be("Tesla", "ORG", 0.95),
						be("Berlin", "LOCATION", 0.88),
					},
				},
				{
					ID: "scene-1", Text: "Musk expanded Tesla across Europe and Elon Musk visited Berlin.",
					Raw: []batteryEntity{
						be("Musk", "PERSON", 0.93),
						be("Elon Musk", "PERSON", 0.97),
						be("Tesla", "ORG", 0.94),
						be("Berlin", "CITY", 0.85),
						be("Volkswagen", "ORG", 0.70),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Elon Musk", "PERSON", "scene-0"),
				bx("Tesla", "ORG", "scene-0"),
				bx("Berlin", "GPE", "scene-0"),
				bx("Elon Musk", "PERSON", "scene-1"),
				bx("Tesla", "ORG", "scene-1"),
				bx("Berlin", "GPE", "scene-1"),
			},
			Rejected: []string{"Volkswagen"},
		},
		{
			ID: "script-08", Category: "automotive", Topic: "Akio Toyoda and Toyota in Japan",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Akio Toyoda led Toyota from Japan for years.",
					Raw: []batteryEntity{
						be("Akio Toyoda", "PERSON", 0.96),
						be("Toyota", "COMPANY", 0.94),
						be("Japan", "COUNTRY", 0.90),
					},
				},
				{
					ID: "scene-1", Text: "Toyoda stepped down while Akio Toyoda kept Toyota influential in Japan.",
					Raw: []batteryEntity{
						be("Toyoda", "PERSON", 0.92),
						be("Akio Toyoda", "PERSON", 0.96),
						be("Toyota", "COMPANY", 0.90),
						be("Japan", "COUNTRY", 0.88),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Akio Toyoda", "PERSON", "scene-0"),
				bx("Toyota", "ORG", "scene-0"),
				bx("Japan", "GPE", "scene-0"),
				bx("Akio Toyoda", "PERSON", "scene-1"),
				bx("Toyota", "ORG", "scene-1"),
				bx("Japan", "GPE", "scene-1"),
			},
		},
	}
}

func technologyScripts() []batteryScript {
	return []batteryScript{
		{
			ID: "script-09", Category: "technology", Topic: "Tim Cook and Apple in Cupertino",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Tim Cook presented new hardware for Apple in Cupertino.",
					Raw: []batteryEntity{
						be("Tim Cook", "PERSON", 0.98),
						be("Apple", "ORG", 0.93),
						be("Cupertino", "CITY", 0.85),
						be("keynote", "KEYWORD", 0.40),
					},
				},
				{
					ID: "scene-1", Text: "Cook praised Cupertino while Tim Cook defended Apple.",
					Raw: []batteryEntity{
						be("Cook", "PERSON", 0.92),
						be("Tim Cook", "PERSON", 0.97),
						be("Cupertino", "CITY", 0.84),
						be("Apple", "ORGANIZATION", 0.90),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Tim Cook", "PERSON", "scene-0"),
				bx("Apple", "ORG", "scene-0"),
				bx("Cupertino", "GPE", "scene-0"),
				bx("Tim Cook", "PERSON", "scene-1"),
				bx("Cupertino", "GPE", "scene-1"),
				bx("Apple", "ORG", "scene-1"),
			},
			Rejected: []string{"keynote"},
		},
		{
			ID: "script-10", Category: "technology", Topic: "Satya Nadella and Microsoft in Redmond",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Satya Nadella transformed Microsoft from Redmond.",
					Raw: []batteryEntity{
						be("Satya Nadella", "PERSON", 0.97),
						be("Microsoft", "ORG", 0.95),
						be("Redmond", "CITY", 0.82),
					},
				},
				{
					ID: "scene-1", Text: "Nadella expanded Microsoft while Satya Nadella hired engineers in Redmond.",
					Raw: []batteryEntity{
						be("Nadella", "PERSON", 0.93),
						be("Satya Nadella", "PERSON", 0.96),
						be("Microsoft", "ORG", 0.94),
						be("Redmond", "CITY", 0.80),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Satya Nadella", "PERSON", "scene-0"),
				bx("Microsoft", "ORG", "scene-0"),
				bx("Redmond", "GPE", "scene-0"),
				bx("Satya Nadella", "PERSON", "scene-1"),
				bx("Microsoft", "ORG", "scene-1"),
				bx("Redmond", "GPE", "scene-1"),
			},
		},
	}
}
