// Package scriptgeneration — entity_battery_corpus_test.go is the GROUND TRUTH
// corpus for the Goal 4 end-to-end entity/overlay validation battery.
//
// The point of the battery is not to test one script: it is to feed the
// generate → extract → normalize → timestamp → overlay chain a deliberately
// HETEROGENEOUS set of topics and measure whether the SAME deterministic
// pipeline keeps producing correct primary entities and correct overlays.
//
// Every script carries, for each scene:
//
//   - the narration text (what the voiceover speaks, and the only surface the
//     TEXT gate may ground on);
//   - the RAW entity list as an external NER provider would emit it, including
//     the hard cases the goal calls out: repeated/partial mentions
//     ("Roman Reigns" then "Reigns"), ambiguous names ("Jordan", "Apple",
//     "Washington"), composite names ("New York City", "European Union"),
//     type-vocabulary variants (LOCATION/CITY/COUNTRY → GPE, COMPANY →
//     ORGANIZATION → ORG), secondary concepts that must never contaminate the
//     primaries, and pure noise that must be rejected (hallucinated entities,
//     VISUAL_SUBJECT/KEYWORD search surfaces, multi-word editorial concepts).
//
// Expected is the MANUAL ground truth of what must survive the whole chain:
// one entry per canonical (name, type) that must appear as an entity
// occurrence in that scene. Rejected lists raw values that must NOT survive.
package scriptgeneration

type batteryEntity struct {
	Value      string
	Type       string
	Confidence float64
}

type batteryScene struct {
	ID   string
	Text string
	Raw  []batteryEntity
}

type batteryExpected struct {
	Name  string
	Type  string
	Scene string
}

type batteryScript struct {
	ID       string
	Category string
	Topic    string
	Scenes   []batteryScene
	Expected []batteryExpected
	// Rejected lists raw extractor values that must NOT survive the
	// grounding/classification gate (ungrounded hallucinations, KEYWORD /
	// VISUAL_SUBJECT search surfaces, multi-word concepts promoted to phrases).
	Rejected []string
}

func be(value, entityType string, confidence float64) batteryEntity {
	return batteryEntity{Value: value, Type: entityType, Confidence: confidence}
}

func bx(name, entityType, scene string) batteryExpected {
	return batteryExpected{Name: name, Type: entityType, Scene: scene}
}

// entityBatteryCorpus returns the 24-script battery across 12 topic
// categories (2 scripts each). It is intentionally data-only: the harness
// owns the execution, this file owns the truth.
func entityBatteryCorpus() []batteryScript {
	return concatScripts(
		wweScripts(),
		sportsScripts(),
		celebrityScripts(),
		automotiveScripts(),
		technologyScripts(),
		historyScripts(),
		geopoliticsScripts(),
		businessScripts(),
		scienceScripts(),
		crimeNewsScripts(),
		immigrationScripts(),
		cinemaScripts(),
	)
}

func concatScripts(groups ...[]batteryScript) []batteryScript {
	var out []batteryScript
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func wweScripts() []batteryScript {
	return []batteryScript{
		{
			ID: "script-01", Category: "WWE", Topic: "Roman Reigns and the Bloodline",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Roman Reigns leads the Bloodline in the WWE.",
					Raw: []batteryEntity{
						be("Roman Reigns", "PERSON", 0.98),
						be("WWE", "ORG", 0.95),
						be("Bloodline", "CONCEPT", 0.80),
						be("ghost champion", "PERSON", 0.60),
						be("PERSON", "VISUAL_SUBJECT", 0.90),
					},
				},
				{
					ID: "scene-1", Text: "Reigns defended the title at WrestleMania against Roman Reigns rivals.",
					Raw: []batteryEntity{
						be("Reigns", "PERSON", 0.91),
						be("Roman Reigns", "PERSON", 0.96),
						be("WrestleMania", "EVENT", 0.85),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Roman Reigns", "PERSON", "scene-0"),
				bx("WWE", "ORG", "scene-0"),
				bx("Bloodline", "CONCEPT", "scene-0"),
				bx("Roman Reigns", "PERSON", "scene-1"),
				bx("WrestleMania", "EVENT", "scene-1"),
			},
			Rejected: []string{"ghost champion", "PERSON"},
		},
		{
			ID: "script-02", Category: "WWE", Topic: "Bianca Belair on SmackDown",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Bianca Belair competed on SmackDown last night.",
					Raw: []batteryEntity{
						be("Bianca Belair", "PERSON", 0.97),
						be("SmackDown", "ORG", 0.72),
						be("wrestling", "KEYWORD", 0.50),
					},
				},
				{
					ID: "scene-1", Text: "Belair won the championship match in front of Bianca Belair fans.",
					Raw: []batteryEntity{
						be("Belair", "PERSON", 0.93),
						be("championship match", "CONCEPT", 0.81),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Bianca Belair", "PERSON", "scene-0"),
				bx("SmackDown", "ORG", "scene-0"),
				bx("Bianca Belair", "PERSON", "scene-1"),
			},
			Rejected: []string{"wrestling", "championship match"},
		},
	}
}

func sportsScripts() []batteryScript {
	return []batteryScript{
		{
			ID: "script-03", Category: "WNBA/NBA", Topic: "Caitlin Clark and the Indiana Fever",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Caitlin Clark plays for the Indiana Fever in the WNBA.",
					Raw: []batteryEntity{
						be("Caitlin Clark", "PERSON", 0.98),
						be("Indiana Fever", "ORGANIZATION", 0.88),
						be("WNBA", "ORG", 0.90),
					},
				},
				{
					ID: "scene-1", Text: "Clark set a rookie record while Caitlin Clark led the Fever.",
					Raw: []batteryEntity{
						be("Clark", "PERSON", 0.92),
						be("Caitlin Clark", "PERSON", 0.97),
						be("Fever", "ORG", 0.70),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Caitlin Clark", "PERSON", "scene-0"),
				bx("Indiana Fever", "ORG", "scene-0"),
				bx("WNBA", "ORG", "scene-0"),
				bx("Caitlin Clark", "PERSON", "scene-1"),
				bx("Fever", "ORG", "scene-1"),
			},
		},
		{
			ID: "script-04", Category: "WNBA/NBA", Topic: "Michael Jordan and the Chicago Bulls",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Michael Jordan redefined the Chicago Bulls in the NBA.",
					Raw: []batteryEntity{
						be("Michael Jordan", "PERSON", 0.99),
						be("Chicago Bulls", "ORG", 0.91),
						be("NBA", "ORG", 0.94),
					},
				},
				{
					ID: "scene-1", Text: "Jordan returned to Chicago while Michael Jordan trained.",
					Raw: []batteryEntity{
						be("Jordan", "PERSON", 0.93),
						be("Michael Jordan", "PERSON", 0.98),
						be("Chicago", "CITY", 0.86),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Michael Jordan", "PERSON", "scene-0"),
				bx("Chicago Bulls", "ORG", "scene-0"),
				bx("NBA", "ORG", "scene-0"),
				bx("Michael Jordan", "PERSON", "scene-1"),
				bx("Chicago", "GPE", "scene-1"),
			},
		},
	}
}

func celebrityScripts() []batteryScript {
	return []batteryScript{
		{
			ID: "script-05", Category: "celebrities", Topic: "Taylor Swift and the Grammy Awards",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Taylor Swift dominated the Grammy Awards in Nashville.",
					Raw: []batteryEntity{
						be("Taylor Swift", "PERSON", 0.99),
						be("Grammy Awards", "EVENT", 0.90),
						be("Nashville", "LOCATION", 0.88),
					},
				},
				{
					ID: "scene-1", Text: "Swift thanked Nashville and Taylor Swift fans worldwide.",
					Raw: []batteryEntity{
						be("Swift", "PERSON", 0.94),
						be("Taylor Swift", "PERSON", 0.98),
						be("Nashville", "LOCATION", 0.85),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Taylor Swift", "PERSON", "scene-0"),
				bx("Grammy Awards", "EVENT", "scene-0"),
				bx("Nashville", "GPE", "scene-0"),
				bx("Taylor Swift", "PERSON", "scene-1"),
				bx("Nashville", "GPE", "scene-1"),
			},
		},
		{
			ID: "script-06", Category: "celebrities", Topic: "Dwayne Johnson and Beyoncé",
			Scenes: []batteryScene{
				{
					ID: "scene-0", Text: "Dwayne Johnson produced a film in Miami for Netflix.",
					Raw: []batteryEntity{
						be("Dwayne Johnson", "PERSON", 0.98),
						be("Netflix", "COMPANY", 0.92),
						be("Miami", "CITY", 0.89),
					},
				},
				{
					ID: "scene-1", Text: "Beyoncé performed in Houston before Dwayne Johnson arrived.",
					Raw: []batteryEntity{
						be("Beyoncé", "PERSON", 0.99),
						be("Houston", "CITY", 0.87),
						be("Johnson", "PERSON", 0.90),
					},
				},
			},
			Expected: []batteryExpected{
				bx("Dwayne Johnson", "PERSON", "scene-0"),
				bx("Netflix", "ORG", "scene-0"),
				bx("Miami", "GPE", "scene-0"),
				bx("Beyoncé", "PERSON", "scene-1"),
				bx("Houston", "GPE", "scene-1"),
				bx("Dwayne Johnson", "PERSON", "scene-1"),
			},
		},
	}
}
