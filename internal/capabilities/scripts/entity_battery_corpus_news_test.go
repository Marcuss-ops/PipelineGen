// Package scriptgeneration — entity_battery_corpus_news_test.go holds the
// science, crime/news, immigration and cinema portions of the Goal 4
// ground-truth corpus.
package scriptgeneration

func scienceScripts() []batteryScript {
	return []batteryScript{
		{
			ID: "script-17", Category: "science", Topic: "NASA, SpaceX and Cape Canaveral",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "NASA partnered with SpaceX at Cape Canaveral.",
					Raw: []batteryEntity{
						be("NASA", "ORG", 0.95),
						be("SpaceX", "ORG", 0.94),
						be("Cape Canaveral", "LOCATION", 0.88),
					},
				},
				{
					ID: "scene-1", Text: "SpaceX launched for NASA from Cape Canaveral.",
					Raw: []batteryEntity{
						be("SpaceX", "ORGANIZATION", 0.93),
						be("NASA", "ORG", 0.94),
						be("Cape Canaveral", "LOCATION", 0.85),
					},
				},
			},
			Expected: []batteryExpected{
				bx("NASA", "ORG", "scene-0"),
				bx("SpaceX", "ORG", "scene-0"),
				bx("Cape Canaveral", "GPE", "scene-0"),
				bx("SpaceX", "ORG", "scene-1"),
				bx("NASA", "ORG", "scene-1"),
				bx("Cape Canaveral", "GPE", "scene-1"),
			},
		},
		{
			ID: "script-18", Category: "science", Topic: "CERN and the Large Hadron Collider",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "CERN operates the Large Hadron Collider near Geneva.",
					Raw: []batteryEntity{
						be("CERN", "ORG", 0.95),
						be("Large Hadron Collider", "ORG", 0.90),
						be("Geneva", "CITY", 0.86),
					},
				},
				{
					ID: "scene-1", Text: "Geneva hosts CERN and the Large Hadron Collider.",
					Raw: []batteryEntity{
						be("Geneva", "CITY", 0.85),
						be("CERN", "ORG", 0.94),
						be("Large Hadron Collider", "ORG", 0.88),
					},
				},
			},
			Expected: []batteryExpected{
				bx("CERN", "ORG", "scene-0"),
				bx("Large Hadron Collider", "ORG", "scene-0"),
				bx("Geneva", "GPE", "scene-0"),
				bx("Geneva", "GPE", "scene-1"),
				bx("CERN", "ORG", "scene-1"),
				bx("Large Hadron Collider", "ORG", "scene-1"),
			},
		},
	}
}

func crimeNewsScripts() []batteryScript {
	return []batteryScript{
		{
			ID: "script-19", Category: "crime/news", Topic: "Christopher Wray and the FBI",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Christopher Wray directed the FBI in Washington.",
					Raw: []batteryEntity{
						be("Christopher Wray", "PERSON", 0.96),
						be("FBI", "ORG", 0.94),
						be("Washington", "CITY", 0.88),
					},
				},
				{
					ID: "scene-1", Text: "Wray defended the FBI while Christopher Wray visited Washington.",
					Raw: []batteryEntity{
						be("Wray", "PERSON", 0.92),
						be("Christopher Wray", "PERSON", 0.96),
						be("FBI", "ORG", 0.93),
						be("Washington", "CITY", 0.86),
						be("shadow informant", "PERSON", 0.55),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Christopher Wray", "PERSON", "scene-0"),
				bx("FBI", "ORG", "scene-0"),
				bx("Washington", "GPE", "scene-0"),
				bx("Christopher Wray", "PERSON", "scene-1"),
				bx("FBI", "ORG", "scene-1"),
				bx("Washington", "GPE", "scene-1"),
			},
			Rejected: []string{"shadow informant"},
		},
		{
			ID: "script-20", Category: "crime/news", Topic: "Interpol and Jürgen Stock",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Jürgen Stock led Interpol from Lyon.",
					Raw: []batteryEntity{
						be("Jürgen Stock", "PERSON", 0.96),
						be("Interpol", "ORG", 0.94),
						be("Lyon", "CITY", 0.86),
					},
				},
				{
					ID: "scene-1", Text: "Stock addressed Interpol while Jürgen Stock returned to Lyon.",
					Raw: []batteryEntity{
						be("Stock", "PERSON", 0.92),
						be("Jürgen Stock", "PERSON", 0.96),
						be("Interpol", "ORGANIZATION", 0.93),
						be("Lyon", "CITY", 0.84),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Jürgen Stock", "PERSON", "scene-0"),
				bx("Interpol", "ORG", "scene-0"),
				bx("Lyon", "GPE", "scene-0"),
				bx("Jürgen Stock", "PERSON", "scene-1"),
				bx("Interpol", "ORG", "scene-1"),
				bx("Lyon", "GPE", "scene-1"),
			},
		},
	}
}

func immigrationScripts() []batteryScript {
	return []batteryScript{
		{
			ID: "script-21", Category: "immigration", Topic: "The United States and Mexico",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "The United States negotiated with Mexico over immigration.",
					Raw: []batteryEntity{
						be("United States", "COUNTRY", 0.94),
						be("Mexico", "COUNTRY", 0.93),
						be("immigration", "CONCEPT", 0.80),
					},
				},
				{
					ID: "scene-1", Text: "Mexico and the United States revised the immigration policy.",
					Raw: []batteryEntity{
						be("Mexico", "COUNTRY", 0.92),
						be("United States", "COUNTRY", 0.93),
						be("immigration", "CONCEPT", 0.78),
					},
				},
			},
			Expected: []batteryExpected{
				bx("United States", "GPE", "scene-0"),
				bx("Mexico", "GPE", "scene-0"),
				bx("immigration", "CONCEPT", "scene-0"),
				bx("Mexico", "GPE", "scene-1"),
				bx("United States", "GPE", "scene-1"),
				bx("immigration", "CONCEPT", "scene-1"),
			},
		},
		{
			ID: "script-22", Category: "immigration", Topic: "Canada, Toronto and Ottawa",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Canada welcomed newcomers to Toronto and Ottawa.",
					Raw: []batteryEntity{
						be("Canada", "COUNTRY", 0.93),
						be("Toronto", "CITY", 0.90),
						be("Ottawa", "CITY", 0.88),
					},
				},
				{
					ID: "scene-1", Text: "Toronto and Ottawa guide Canada policy.",
					Raw: []batteryEntity{
						be("Toronto", "CITY", 0.89),
						be("Ottawa", "CITY", 0.87),
						be("Canada", "COUNTRY", 0.92),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Canada", "GPE", "scene-0"),
				bx("Toronto", "GPE", "scene-0"),
				bx("Ottawa", "GPE", "scene-0"),
				bx("Toronto", "GPE", "scene-1"),
				bx("Ottawa", "GPE", "scene-1"),
				bx("Canada", "GPE", "scene-1"),
			},
		},
	}
}

func cinemaScripts() []batteryScript {
	return []batteryScript{
		{
			ID: "script-23", Category: "cinema", Topic: "Christopher Nolan and Oppenheimer",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Christopher Nolan directed Oppenheimer in Hollywood.",
					Raw: []batteryEntity{
						be("Christopher Nolan", "PERSON", 0.98),
						be("Oppenheimer", "WORK_OF_ART", 0.90),
						be("Hollywood", "LOCATION", 0.85),
					},
				},
				{
					ID: "scene-1", Text: "Nolan praised Hollywood while Christopher Nolan discussed Oppenheimer.",
					Raw: []batteryEntity{
						be("Nolan", "PERSON", 0.93),
						be("Christopher Nolan", "PERSON", 0.97),
						be("Hollywood", "LOCATION", 0.84),
						be("Oppenheimer", "WORK_OF_ART", 0.88),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Christopher Nolan", "PERSON", "scene-0"),
				bx("Oppenheimer", "WORK_OF_ART", "scene-0"),
				bx("Hollywood", "GPE", "scene-0"),
				bx("Christopher Nolan", "PERSON", "scene-1"),
				bx("Hollywood", "GPE", "scene-1"),
				bx("Oppenheimer", "WORK_OF_ART", "scene-1"),
			},
		},
		{
			ID: "script-24", Category: "cinema", Topic: "Fernando Meirelles and São Paulo",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Fernando Meirelles filmed in São Paulo and Brazil.",
					Raw: []batteryEntity{
						be("Fernando Meirelles", "PERSON", 0.96),
						be("São Paulo", "CITY", 0.90),
						be("Brazil", "COUNTRY", 0.89),
					},
				},
				{
					ID: "scene-1", Text: "São Paulo honored Brazil and Fernando Meirelles.",
					Raw: []batteryEntity{
						be("São Paulo", "CITY", 0.88),
						be("Brazil", "COUNTRY", 0.87),
						be("Fernando Meirelles", "PERSON", 0.95),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Fernando Meirelles", "PERSON", "scene-0"),
				bx("São Paulo", "GPE", "scene-0"),
				bx("Brazil", "GPE", "scene-0"),
				bx("São Paulo", "GPE", "scene-1"),
				bx("Brazil", "GPE", "scene-1"),
				bx("Fernando Meirelles", "PERSON", "scene-1"),
			},
		},
	}
}
