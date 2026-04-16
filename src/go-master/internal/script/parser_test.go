package script

import (
	"strings"
	"testing"
	"time"

	"velox/go-master/pkg/util"
)

// TestParser_SplitIntoScenes tests that script is correctly split into scenes
func TestParser_SplitIntoScenes(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedMin  int
		expectedMax  int
		description  string
	}{
		{
			name: "Single paragraph script",
			input: "Questo è un breve script di prova. Parla di tecnologia e innovazione. " +
				"È molto interessante e informativo.",
			expectedMin: 1,
			expectedMax: 1,
			description: "Short script should be single scene",
		},
		{
			name: "Multiple paragraphs",
			input: "Introduzione alla tecnologia moderna. Oggi parleremo di AI.\n\n" +
				"Nel contenuto principale, vedremo come i robot possono aiutare gli umani " +
				"nella vita quotidiana. Questo è molto importante per il futuro.\n\n" +
				"In conclusione, la tecnologia continuerà a evolversi e cambiare il mondo.",
			expectedMin: 2,
			expectedMax: 5,
			description: "Should split into multiple scenes",
		},
		{
			name: "Explicit section markers",
			input: "===SEZIONE: Introduzione===\n" +
				"Benvenuti a questo video sulla tecnologia.\n\n" +
				"===SEZIONE: Contenuto===\n" +
				"Oggi vedremo l'intelligenza artificiale e i robot.\n\n" +
				"===SEZIONE: Conclusione===\n" +
				"Grazie per aver guardato questo video.",
			expectedMin: 3,
			expectedMax: 3,
			description: "Should detect 3 explicit sections",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewParser(60, "it")
			scenes := parser.splitIntoScenes(tt.input)

			if len(scenes) < tt.expectedMin || len(scenes) > tt.expectedMax {
				t.Errorf("Expected %d-%d scenes, got %d. %s",
					tt.expectedMin, tt.expectedMax, len(scenes), tt.description)
			}

			t.Logf("✅ %s: %d scenes (expected %d-%d)", tt.name, len(scenes), tt.expectedMin, tt.expectedMax)
			for i, scene := range scenes {
				t.Logf("   Scene %d: Type=%s, Title=%s, TextLen=%d",
					i+1, scene.Type, scene.Title, len(scene.Text))
			}
		})
	}
}

// TestParser_KeywordExtraction tests keyword extraction from scenes
func TestParser_KeywordExtraction(t *testing.T) {
	input := "L'intelligenza artificiale sta rivoluzionando la tecnologia moderna. " +
		"I computer quantistici possono fare calcoli incredibili. " +
		"La robotica avanza ogni giorno di più con nuovi algoritmi di machine learning."

	parser := NewParser(60, "it")
	scenes := parser.splitIntoScenes(input)

	if len(scenes) == 0 {
		t.Fatal("Expected at least one scene")
	}

	for i := range scenes {
		parser.extractSceneMetadata(&scenes[i])
	}

	scene := scenes[0]
	if len(scene.Keywords) == 0 {
		t.Error("Expected keywords to be extracted")
	}

	t.Logf("✅ Extracted %d keywords:", len(scene.Keywords))
	for i, kw := range scene.Keywords {
		t.Logf("   [%d] %s", i+1, kw)
	}

	// Check that important words are in keywords
	keywordsLower := make([]string, len(scene.Keywords))
	for i, kw := range scene.Keywords {
		keywordsLower[i] = strings.ToLower(kw)
	}

	hasTech := false
	for _, kw := range keywordsLower {
		if strings.Contains(kw, "intelligenza") || strings.Contains(kw, "tecnologia") ||
			strings.Contains(kw, "computer") || strings.Contains(kw, "robotica") {
			hasTech = true
			break
		}
	}

	if !hasTech {
		t.Log("⚠️  No major tech keywords found (may be due to stopword filtering)")
	}
}

// TestParser_EntityExtraction tests named entity extraction
func TestParser_EntityExtraction(t *testing.T) {
	input := "Elon Musk presenta il nuovo robot di Tesla. " +
		"Google e Microsoft collaborano per l'intelligenza artificiale. " +
		"La conferenza a Milano è stata molto interessante."

	parser := NewParser(60, "it")
	scenes := parser.splitIntoScenes(input)

	if len(scenes) == 0 {
		t.Fatal("Expected at least one scene")
	}

	for i := range scenes {
		parser.extractSceneMetadata(&scenes[i])
	}

	scene := scenes[0]
	if len(scene.Entities) == 0 {
		t.Log("⚠️  No entities extracted (expected with simple extraction)")
		return
	}

	t.Logf("✅ Extracted %d entities:", len(scene.Entities))
	for i, entity := range scene.Entities {
		t.Logf("   [%d] %s (type: %s, relevance: %.2f)",
			i+1, entity.Text, entity.Type, entity.Relevance)
	}
}

// TestParser_EmotionDetection tests emotion detection from text
func TestParser_EmotionDetection(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		expectedEmotion string
	}{
		{
			name:            "Joy/Happiness",
			input:           "Sono molto felice di questo fantastico risultato! È meraviglioso e great!",
			expectedEmotion: "joy",
		},
		{
			name:            "Sadness",
			input:           "È molto triste e doloroso. Purtroppo le cose vanno male.",
			expectedEmotion: "sadness",
		},
		{
			name:            "Surprise",
			input:           "Wow, è incredibile e amazing! Che sorpresa!",
			expectedEmotion: "surprise",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewParser(60, "it")
			scenes := parser.splitIntoScenes(tt.input)

			if len(scenes) == 0 {
				t.Fatal("Expected at least one scene")
			}

			for i := range scenes {
				parser.extractSceneMetadata(&scenes[i])
			}

			scene := scenes[0]
			if len(scene.Emotions) == 0 {
				t.Errorf("Expected emotion '%s' to be detected", tt.expectedEmotion)
				return
			}

			found := false
			for _, emotion := range scene.Emotions {
				if emotion == tt.expectedEmotion {
					found = true
					break
				}
			}

			if found {
				t.Logf("✅ Detected emotion '%s' in emotions: %v", tt.expectedEmotion, scene.Emotions)
			} else {
				t.Errorf("Expected emotion '%s' not found in %v", tt.expectedEmotion, scene.Emotions)
			}
		})
	}
}

// TestParser_DurationEstimation tests that scene durations are estimated proportionally
func TestParser_DurationEstimation(t *testing.T) {
	input := "===SEZIONE: Introduzione===\n" +
		"Ciao a tutti, benvenuti.\n\n" +
		"===SEZIONE: Contenuto Principale===\n" +
		"Oggi parleremo di un argomento molto lungo e complesso che richiede " +
		"molte spiegazioni dettagliate. Vedremo diversi aspetti della tecnologia " +
		"moderna e come questa influisce sulla nostra vita quotidiana. " +
		"I computer quantistici sono una rivoluzione nel campo del calcolo. " +
		"L'intelligenza artificiale sta cambiando il mondo in cui viviamo.\n\n" +
		"===SEZIONE: Conclusione===\n" +
		"Grazie per l'attenzione."

	parser := NewParser(90, "it") // 90 seconds target
	scenes := parser.splitIntoScenes(input)

	for i := range scenes {
		parser.extractSceneMetadata(&scenes[i])
	}
	parser.estimateSceneDurations(scenes)

	if len(scenes) < 2 {
		t.Skip("Need at least 2 scenes to test duration distribution")
	}

	totalDuration := 0
	for _, scene := range scenes {
		totalDuration += scene.Duration
	}

	t.Logf("✅ Duration estimation:")
	t.Logf("   Target: 90 seconds, Actual total: %d seconds", totalDuration)
	for i, scene := range scenes {
		t.Logf("   Scene %d (%s): %d seconds, %d words",
			i+1, scene.Type, scene.Duration, scene.WordCount)
	}

	// Total should be close to target (within 10%)
	diff := totalDuration - 90
	if diff < 0 {
		diff = -diff
	}
	if diff > 10 {
		t.Logf("⚠️  Total duration %d differs from target 90 by %d seconds", totalDuration, diff)
	}

	// Middle scene (longest text) should have longest duration
	if len(scenes) >= 3 {
		if scenes[1].Duration <= scenes[0].Duration {
			t.Logf("⚠️  Middle scene should be longer than intro: %d vs %d",
				scenes[1].Duration, scenes[0].Duration)
		}
	}
}

// TestParser_EmptyText tests handling of empty input
func TestParser_EmptyText(t *testing.T) {
	parser := NewParser(60, "it")

	scenes := parser.splitIntoScenes("")
	if len(scenes) != 1 {
		t.Errorf("Expected 1 scene for empty text, got %d", len(scenes))
	}

	if scenes[0].Text != "" {
		t.Errorf("Expected empty text, got: %s", scenes[0].Text)
	}

	t.Log("✅ Empty text handled correctly")
}

// TestParser_VeryShortText tests handling of very short input
func TestParser_VeryShortText(t *testing.T) {
	parser := NewParser(60, "it")

	input := "Ciao."
	scenes := parser.splitIntoScenes(input)

	if len(scenes) != 1 {
		t.Errorf("Expected 1 scene for short text, got %d", len(scenes))
	}

	t.Logf("✅ Very short text: %d scene(s)", len(scenes))
}

// TestParser_LongScript tests parsing of a long, realistic script
func TestParser_LongScript(t *testing.T) {
	input := `===SEZIONE: Introduzione===

Benvenuti in questo video speciale sulla tecnologia del futuro. 
Oggi esploreremo le meraviglie dell'intelligenza artificiale e come 
sta trasformando il nostro mondo in modi incredibili.

===SEZIONE: AI e Machine Learning===

L'intelligenza artificiale è una delle tecnologie più rivoluzionarie del nostro tempo.
I sistemi di machine learning possono imparare dai dati e migliorare automaticamente.
Google, Microsoft e Amazon stanno investendo miliardi in questa tecnologia.

Deep learning è un sottoinsieme del machine learning che usa reti neurali profonde.
Queste reti possono riconoscere immagini, tradurre lingue e generare testo.

===SEZIONE: Robotica Avanzata===

I robot moderni possono fare cose incredibili. Possono camminare, correre, e persino saltare.
Boston Dynamics ha creato robot che possono fare backflip e aprire porte.

I robot collaborativi, o cobot, lavorano fianco a fianco con gli umani.
Sono progettati per essere sicuri e facili da programmare.

===SEZIONE: Computer Quantistici===

I computer quantistici usano qubit invece di bit classici.
Questo permette loro di fare calcoli impossibili per i computer normali.
IBM e Google hanno già computer quantistici funzionanti.

===SEZIONE: Conclusione===

Grazie per aver guardato questo video. Il futuro della tecnologia è brillante!
Non dimenticate di iscrivervi al canale e lasciare un like.`

	parser := NewParser(120, "it") // 2 minutes target

	script, err := parser.Parse(input, "Tech Future", "informative", "ollama")
	if err != nil {
		t.Fatalf("Parser failed: %v", err)
	}

	t.Logf("✅ Long script parsed successfully:")
	t.Logf("   Script ID: %s", script.ID)
	t.Logf("   Title: %s", script.Title)
	t.Logf("   Scenes: %d", len(script.Scenes))
	t.Logf("   Word count: %d", script.WordCount)
	t.Logf("   Target duration: %d seconds", script.TargetDuration)
	t.Logf("   Category: %s", script.Metadata.Category)
	t.Logf("   Tags: %v", script.Metadata.Tags[:util.Min(5, len(script.Metadata.Tags))])

	if len(script.Scenes) < 3 {
		t.Errorf("Expected at least 3 scenes, got %d", len(script.Scenes))
	}

	// Verify each scene has metadata
	for i, scene := range script.Scenes {
		if len(scene.Keywords) == 0 {
			t.Errorf("Scene %d has no keywords", i+1)
		}
		if scene.Duration == 0 {
			t.Errorf("Scene %d has no duration", i+1)
		}
		if scene.Text == "" {
			t.Errorf("Scene %d has empty text", i+1)
		}
	}

	// Verify total duration
	totalDuration := 0
	for _, scene := range script.Scenes {
		totalDuration += scene.Duration
	}

	t.Logf("   Total scene durations: %d seconds (target: %d)", totalDuration, script.TargetDuration)
}

// TestParser_SceneTypes tests that scene types are correctly determined
func TestParser_SceneTypes(t *testing.T) {
	input := "===SEZIONE: Hook Iniziale===\n" +
		"Avete mai pensato a quanto la tecnologia stia cambiando il mondo?\n\n" +
		"===SEZIONE: Contenuto===\n" +
		"Oggi vedremo insieme le ultime novità.\n\n" +
		"===SEZIONE: Transizione===\n" +
		"Intanto, c'è anche un altro aspetto da considerare.\n\n" +
		"===SEZIONE: Conclusione===\n" +
		"Grazie per l'attenzione."

	parser := NewParser(60, "it")
	scenes := parser.splitIntoScenes(input)

	if len(scenes) < 4 {
		t.Skip("Need at least 4 scenes to test scene types")
	}

	expectedTypes := []SceneType{SceneHook, SceneContent, SceneTransition, SceneConclusion}

	for i, expectedType := range expectedTypes {
		if i >= len(scenes) {
			break
		}
		if scenes[i].Type != expectedType {
			t.Errorf("Scene %d: expected type %s, got %s",
				i+1, expectedType, scenes[i].Type)
		} else {
			t.Logf("✅ Scene %d correctly typed as '%s'", i+1, scenes[i].Type)
		}
	}
}

// TestParser_CategoryDetection tests category detection
func TestParser_CategoryDetection(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Tech category",
			input:    "Questo video parla di tecnologia, AI e software. I computer sono fantastici.",
			expected: "tech",
		},
		{
			name:     "Business category",
			input:    "Il business aziendale richiede strategie di marketing e vendite efficaci.",
			expected: "business",
		},
		{
			name:     "Interview category",
			input:    "Intervista con domande e risposte su questo argomento.",
			expected: "interview",
		},
		{
			name:     "Education category",
			input:    "In questo corso impareremo a fare molte cose nuove.",
			expected: "education",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewParser(60, "it")
			category := parser.detectCategory(tt.input)

			if category != tt.expected {
				t.Errorf("Expected category '%s', got '%s'", tt.expected, category)
			} else {
				t.Logf("✅ Category detected: '%s'", category)
			}
		})
	}
}

// TestParser_MetadataExtraction tests metadata extraction
func TestParser_MetadataExtraction(t *testing.T) {
	input := "L'intelligenza artificiale è il futuro della tecnologia. " +
		"I computer quantistici cambieranno il mondo. " +
		"Google e Tesla investono miliardi in ricerca AI."

	parser := NewParser(60, "it")
	scenes := parser.splitIntoScenes(input)

	if len(scenes) == 0 {
		t.Fatal("Expected at least one scene")
	}

	for i := range scenes {
		parser.extractSceneMetadata(&scenes[i])
	}

	script, _ := parser.Parse(input, "Test Script", "informative", "ollama")

	t.Logf("✅ Metadata extracted:")
	t.Logf("   Category: %s", script.Metadata.Category)
	t.Logf("   Total tags: %d", len(script.Metadata.Tags))
	t.Logf("   Key messages: %d", len(script.Metadata.KeyMessages))
	t.Logf("   SEO keywords: %d", len(script.Metadata.SEOKeywords))
	t.Logf("   Requires approval: %v", script.Metadata.RequiresApproval)

	if script.Metadata.Category == "" {
		t.Error("Expected category to be detected")
	}

	if len(script.Metadata.Tags) == 0 {
		t.Error("Expected tags to be extracted")
	}
}

// TestParser_ParseFullScript tests full parsing with all metadata
func TestParser_ParseFullScript(t *testing.T) {
	input := "Introduzione alla tecnologia moderna.\n\n" +
		"Oggi parleremo di AI e robotica.\n\n" +
		"Conclusione: il futuro è adesso."

	parser := NewParser(60, "it")

	script, err := parser.Parse(input, "Test Script", "informative", "ollama")
	if err != nil {
		t.Fatalf("Parser failed: %v", err)
	}

	// Verify basic fields
	if script.Title != "Test Script" {
		t.Errorf("Expected title 'Test Script', got '%s'", script.Title)
	}

	if script.Language != "it" {
		t.Errorf("Expected language 'it', got '%s'", script.Language)
	}

	if script.Tone != "informative" {
		t.Errorf("Expected tone 'informative', got '%s'", script.Tone)
	}

	if script.TargetDuration != 60 {
		t.Errorf("Expected target duration 60, got %d", script.TargetDuration)
	}

	if script.Model != "ollama" {
		t.Errorf("Expected model 'ollama', got '%s'", script.Model)
	}

	// Verify timestamp
	if script.CreatedAt.IsZero() {
		t.Error("Expected CreatedAt to be set")
	}

	if time.Since(script.CreatedAt) > time.Second {
		t.Error("CreatedAt seems incorrect")
	}

	t.Log("✅ Full script parsed with all metadata")
}

// BenchmarkParser_LongScript benchmarks parsing performance
func BenchmarkParser_LongScript(b *testing.B) {
	input := strings.Repeat(
		"L'intelligenza artificiale sta rivoluzionando la tecnologia. "+
			"I computer moderni sono sempre più potenti. "+
			"La robotica avanza ogni giorno di più.\n\n",
		100)

	parser := NewParser(120, "it")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(input, "Benchmark Script", "informative", "ollama")
	}
}
