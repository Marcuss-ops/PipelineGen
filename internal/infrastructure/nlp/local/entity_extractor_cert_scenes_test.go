package local

import (
	"context"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// certScene is one controlled scene from the NLP certification payload.
// The local extractor is CPU-only and deterministic, so every assertion below
// pins the exact current output: one person, the exact GPE set, exactly one
// important phrase and the exact 3-5 important words.
//
// Single-word places ("London") are detected through the knownPlaces lexicon,
// so they are asserted exactly like the multi-word places (Los Angeles,
// New York, United States).
type certScene struct {
	name   string
	source string
	person string
	places []string
	phrase string
	words  []string
}

func extractEN(t *testing.T, text string) *scriptpkg.EntityResult {
	t.Helper()
	result, err := NewExtractor().ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{
		Text:        text,
		Language:    "en",
		EntityCount: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func requirePerson(t *testing.T, result *scriptpkg.EntityResult, want string) {
	t.Helper()
	if len(result.Persons) != 1 {
		t.Fatalf("persons = %+v, want exactly one person", result.Persons)
	}
	if result.Persons[0].Value != want {
		t.Fatalf("persons = %+v, want PERSON %q", result.Persons, want)
	}
}

func requirePlaces(t *testing.T, result *scriptpkg.EntityResult, want []string) {
	t.Helper()
	if len(result.Places) != len(want) {
		t.Fatalf("places = %+v, want %v", result.Places, want)
	}
	for i, p := range result.Places {
		if p.Value != want[i] || p.Type != "GPE" {
			t.Fatalf("places = %+v, want GPE %v", result.Places, want)
		}
	}
}

func requireWords(t *testing.T, result *scriptpkg.EntityResult, want []string) {
	t.Helper()
	if len(result.ImportantWords) != len(want) {
		t.Fatalf("important words = %v (len %d), want %v", result.ImportantWords, len(result.ImportantWords), want)
	}
	for i, w := range result.ImportantWords {
		if w != want[i] {
			t.Fatalf("important words = %v, want %v", result.ImportantWords, want)
		}
	}
}

func TestExtractorCertScenesDeterministic(t *testing.T) {
	scenes := []certScene{
		{
			name:   "scene-dwayne",
			source: "Dwayne Johnson trained in Los Angeles. In 2025, Dwayne Johnson described professional wrestling as a discipline built on athletic training, dramatic storytelling, audience connection, and repeated performance under pressure. Wrestling demands training, storytelling, discipline, performance, and resilience.",
			person: "Dwayne Johnson",
			places: []string{"Los Angeles"},
			phrase: "In 2025, Dwayne Johnson described professional wrestling as a discipline built on athletic training, dramatic storytelling, audience connection, and repeated performance under pressure.",
			words:  []string{"storytelling", "performance", "professional", "discipline", "wrestling"},
		},
		{
			name:   "scene-serena",
			source: "Serena Williams appears in New York. In 2024, Serena Williams described competitive tennis as a combination of disciplined training, mental resilience, tactical preparation, and consistent performance. Tennis rewards preparation, resilience, discipline, competition, and precision.",
			person: "Serena Williams",
			places: []string{"New York"},
			phrase: "In 2024, Serena Williams described competitive tennis as a combination of disciplined training, mental resilience, tactical preparation, and consistent performance.",
			words:  []string{"preparation", "resilience", "competitive", "combination", "disciplined"},
		},
		{
			name:   "scene-tom",
			source: "Tom Holland works in London. In 2025, Tom Holland described acting as a craft requiring preparation, character development, emotional control, creative storytelling, and repeated performance. Acting depends on storytelling, preparation, creativity, character, and performance.",
			person: "Tom Holland",
			places: []string{"London"},
			phrase: "In 2025, Tom Holland described acting as a craft requiring preparation, character development, emotional control, creative storytelling, and repeated performance.",
			words:  []string{"storytelling", "preparation", "performance", "development", "character"},
		},
		{
			name:   "scene-gordon",
			source: "Gordon Ramsay works in London. In 2025, Gordon Ramsay described professional cooking as a discipline combining kitchen technique, ingredient knowledge, preparation, precision, and consistent execution. Cooking requires technique, preparation, precision, ingredients, and discipline.",
			person: "Gordon Ramsay",
			places: []string{"London"},
			phrase: "In 2025, Gordon Ramsay described professional cooking as a discipline combining kitchen technique, ingredient knowledge, preparation, precision, and consistent execution.",
			words:  []string{"preparation", "professional", "discipline", "ingredients", "technique"},
		},
		{
			name:   "scene-adele",
			source: "Adele Adkins performs in London. In 2025, Adele Adkins described music as a combination of vocal technique, songwriting, emotional storytelling, careful performance, and audience connection. Music depends on songwriting, vocals, storytelling, performance, and emotion.",
			person: "Adele Adkins",
			places: []string{"London"},
			phrase: "In 2025, Adele Adkins described music as a combination of vocal technique, songwriting, emotional storytelling, careful performance, and audience connection.",
			words:  []string{"storytelling", "songwriting", "performance", "combination", "connection"},
		},
		{
			name:   "scene-keanu",
			source: "Keanu Reeves works in Los Angeles. In 2025, Keanu Reeves described cinema as a collaborative craft involving physical preparation, character work, stunt training, visual storytelling, and disciplined performance. Cinema requires preparation, storytelling, training, character, and performance.",
			person: "Keanu Reeves",
			places: []string{"Los Angeles"},
			phrase: "In 2025, Keanu Reeves described cinema as a collaborative craft involving physical preparation, character work, stunt training, visual storytelling, and disciplined performance.",
			words:  []string{"storytelling", "collaborative", "preparation", "performance", "disciplined"},
		},
		{
			name:   "scene-lewis",
			source: "Lewis Hamilton appears in London. In 2025, Lewis Hamilton described racing as a discipline combining engineering knowledge, strategic preparation, physical concentration, technical precision, and consistent performance. Racing depends on strategy, engineering, preparation, precision, and performance.",
			person: "Lewis Hamilton",
			places: []string{"London"},
			phrase: "In 2025, Lewis Hamilton described racing as a discipline combining engineering knowledge, strategic preparation, physical concentration, technical precision, and consistent performance.",
			words:  []string{"concentration", "engineering", "preparation", "performance", "precision"},
		},
		{
			name:   "scene-taylor",
			source: "Taylor Swift appears in New York. In 2025, Taylor Swift described songwriting as a process combining language, musical structure, emotional storytelling, creative revision, and live performance. Songwriting depends on storytelling, creativity, language, revision, and performance.",
			person: "Taylor Swift",
			places: []string{"New York"},
			phrase: "In 2025, Taylor Swift described songwriting as a process combining language, musical structure, emotional storytelling, creative revision, and live performance.",
			words:  []string{"storytelling", "songwriting", "performance", "creativity", "language"},
		},
		{
			name:   "scene-emma",
			source: "Emma Watson appears in London. In 2025, Emma Watson described education as a process involving communication, research, learning, critical thinking, and sustained personal development. Education depends on learning, communication, research, development, and thinking.",
			person: "Emma Watson",
			places: []string{"London"},
			phrase: "In 2025, Emma Watson described education as a process involving communication, research, learning, critical thinking, and sustained personal development.",
			words:  []string{"communication", "development", "education", "research", "learning"},
		},
		{
			name:   "scene-messi",
			source: "Lionel Messi appears in the United States. In 2025, Lionel Messi described football as a sport combining technical control, tactical awareness, coordinated teamwork, physical preparation, and consistent performance. Football depends on technique, teamwork, preparation, awareness, and performance.",
			person: "Lionel Messi",
			places: []string{"United States"},
			phrase: "In 2025, Lionel Messi described football as a sport combining technical control, tactical awareness, coordinated teamwork, physical preparation, and consistent performance.",
			words:  []string{"preparation", "performance", "coordinated", "awareness", "consistent"},
		},
	}

	for _, sc := range scenes {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			result := extractEN(t, sc.source)

			// Deterministic extraction: run twice and require identical output.
			if again := extractEN(t, sc.source); !equalEntityResult(result, again) {
				t.Fatalf("extractor is not deterministic for %s", sc.name)
			}

			requirePerson(t, result, sc.person)
			requirePlaces(t, result, sc.places)

			if len(result.ImportantPhrases) != 1 {
				t.Fatalf("important phrases = %+v, want exactly one", result.ImportantPhrases)
			}
			if result.ImportantPhrases[0] != sc.phrase {
				t.Fatalf("phrase = %q, want %q", result.ImportantPhrases[0], sc.phrase)
			}

			requireWords(t, result, sc.words)
		})
	}
}

func equalEntityResult(a, b *scriptpkg.EntityResult) bool {
	if len(a.Persons) != len(b.Persons) || len(a.Places) != len(b.Places) ||
		len(a.ImportantPhrases) != len(b.ImportantPhrases) || len(a.ImportantWords) != len(b.ImportantWords) {
		return false
	}
	for i := range a.Persons {
		if a.Persons[i] != b.Persons[i] {
			return false
		}
	}
	for i := range a.Places {
		if a.Places[i] != b.Places[i] {
			return false
		}
	}
	for i := range a.ImportantPhrases {
		if a.ImportantPhrases[i] != b.ImportantPhrases[i] {
			return false
		}
	}
	for i := range a.ImportantWords {
		if a.ImportantWords[i] != b.ImportantWords[i] {
			return false
		}
	}
	return true
}
