package script

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// TEST DI STRESS - Script Parser + Clip Mapper
// ============================================================================

// Test 1: Density tecnica - verifica keywords, entità, differentiation concetti
func TestStress_TechnicalKeywordDensity(t *testing.T) {
	input := "Il futuro del calcolo quantistico e l'integrazione con i chip neuromorfici nel 2026. " +
		"Analizza come i qubit superconduttori stiano superando i limiti del silicio tradizionale, " +
		"permettendo alle AI di elaborare dati a una velocità mai vista prima. " +
		"Mostra laboratori di ricerca, server farm raffreddate a liquido e simulazioni molecolari."

	title := "Quantum Computing & Neuromorphic Chips 2026"
	tone := "professional"
	targetDuration := 120 // secondi

	parser := NewParser(targetDuration, "italian")
	startTime := time.Now()

	script, err := parser.Parse(input, title, tone, "gemma3:4b")
	if err != nil {
		t.Fatalf("Parser failed: %v", err)
	}

	elapsed := time.Since(startTime)

	// --- Verifica 1: Struttura scene ---
	t.Run("StructureCheck", func(t *testing.T) {
		if len(script.Scenes) == 0 {
			t.Fatal("NO scenes generated")
		}

		t.Logf("✅ Generated %d scene in %v", len(script.Scenes), elapsed)

		for _, scene := range script.Scenes {
			t.Logf("\n--- Scene %d [%s] (durata stimata: %ds) ---",
				scene.SceneNumber, scene.Type, scene.Duration)
			t.Logf("  Keywords (%d): %v", len(scene.Keywords), scene.Keywords)
			t.Logf("  Entities (%d):", len(scene.Entities))
			for _, e := range scene.Entities {
				t.Logf("    - %s (type: %s, relevance: %.2f)", e.Text, e.Type, e.Relevance)
			}
			t.Logf("  Emotions (%d): %v", len(scene.Emotions), scene.Emotions)
			t.Logf("  Visual Cues (%d): %v", len(scene.VisualCues), scene.VisualCues)
		}
	})

	// --- Verifica 2: Keywords tecniche specifiche ---
	t.Run("TechnicalKeywords", func(t *testing.T) {
		allKeywords := map[string]bool{}
		for _, scene := range script.Scenes {
			for _, kw := range scene.Keywords {
				allKeyword := strings.ToLower(kw)
				allKeywords[allKeyword] = true
			}
		}

		// Keywords che DEVONO essere presenti
		requiredConcepts := []string{
			"quantistico", "quantum", "qubit", "neuromorfici",
			"silicio", "server", "farm", "laboratori",
		}

		foundCount := 0
		for _, concept := range requiredConcepts {
			for kw := range allKeywords {
				if strings.Contains(kw, concept) {
					foundCount++
					t.Logf("✅ Found concept '%s' in keywords as '%s'", concept, kw)
					break
				}
			}
		}

		if foundCount < 4 {
			t.Errorf("Only %d/%d required concepts found in keywords. Need at least 4.",
				foundCount, len(requiredConcepts))
		} else {
			t.Logf("✅ %d/%d required concepts found", foundCount, len(requiredConcepts))
		}
	})

	// --- Verifica 3: Entità tecniche ---
	t.Run("TechnicalEntities", func(t *testing.T) {
		allEntities := map[string]SceneEntity{}
		for _, scene := range script.Scenes {
			for _, e := range scene.Entities {
				allEntities[strings.ToLower(e.Text)] = e
			}
		}

		t.Logf("Total unique entities: %d", len(allEntities))

		// Verifica che entità tecniche siano distinte
		technicalTerms := []string{"2026", "AI"}
		for _, term := range technicalTerms {
			found := false
			for entity := range allEntities {
				if strings.Contains(entity, strings.ToLower(term)) {
					found = true
					break
				}
			}
			if found {
				t.Logf("✅ Found entity '%s'", term)
			} else {
				t.Logf("⚠️  Entity '%s' not found (might be OK for generic terms)", term)
			}
		}
	})

	// --- Verifica 4: JSON serialization ---
	t.Run("JSONSerialization", func(t *testing.T) {
		data, err := json.MarshalIndent(script, "", "  ")
		if err != nil {
			t.Fatalf("Failed to marshal script: %v", err)
		}

		// Verifica che JSON contenga campi attesi
		jsonStr := string(data)
		requiredFields := []string{
			"scene_number", "keywords", "entities", "emotions",
			"duration", "clip_mapping", "scene_type",
		}

		for _, field := range requiredFields {
			if strings.Contains(jsonStr, field) {
				t.Logf("✅ JSON contains field '%s'", field)
			} else {
				t.Errorf("❌ JSON missing field '%s'", field)
			}
		}

		t.Logf("JSON size: %d bytes (%.1f KB)", len(data), float64(len(data))/1024)
	})

	// --- Verifica 5: Performance ---
	t.Run("Performance", func(t *testing.T) {
		if elapsed > 5*time.Second {
			t.Errorf("❌ Parser took %v (expected < 5s)", elapsed)
		} else {
			t.Logf("✅ Parser completed in %v", elapsed)
		}
	})

	// --- Output JSON completo ---
	t.Logf("\n========== FULL SCRIPT JSON ==========")
	data, _ := json.MarshalIndent(script, "", "  ")
	t.Logf("%s", string(data))
	t.Logf("========================================")
}

// Test 2: Emozionale/Cinematico - verifica cambio di tono
func TestStress_EmotionalCinematic(t *testing.T) {
	input := "Inizia con una riflessione malinconica sulla solitudine nelle grandi città, " +
		"con pioggia sui vetri e strade vuote all'alba. " +
		"Poi, improvvisamente, il ritmo cambia: diventa energico, solare e motivazionale. " +
		"Mostra persone che corrono, iniziano a lavorare con il sorriso e il sole che sorge tra i grattacieli."

	title := "From Melancholy to Energy"
	tone := "cinematic"
	targetDuration := 90

	parser := NewParser(targetDuration, "italian")
	startTime := time.Now()

	script, err := parser.Parse(input, title, tone, "gemma3:4b")
	if err != nil {
		t.Fatalf("Parser failed: %v", err)
	}

	elapsed := time.Since(startTime)

	// --- Verifica 1: Emozioni per scena ---
	t.Run("EmotionTransition", func(t *testing.T) {
		if len(script.Scenes) == 0 {
			t.Fatal("NO scenes generated")
		}

		t.Logf("✅ Generated %d scene in %v", len(script.Scenes), elapsed)

		hasSadness := false
		hasJoy := false
		sadnessScene := 0
		joyScene := 0

		for _, scene := range script.Scenes {
			t.Logf("\n--- Scene %d ---", scene.SceneNumber)
			t.Logf("  Emotions: %v", scene.Emotions)

			for _, emotion := range scene.Emotions {
				if emotion == "sadness" {
					hasSadness = true
					sadnessScene = scene.SceneNumber
				}
				if emotion == "joy" {
					hasJoy = true
					joyScene = scene.SceneNumber
				}
			}
		}

		if hasSadness {
			t.Logf("✅ 'sadness' emotion found in Scene %d", sadnessScene)
		} else {
			t.Logf("⚠️  'sadness' emotion NOT found (might need better detection)")
		}

		if hasJoy {
			t.Logf("✅ 'joy' emotion found in Scene %d", joyScene)
		} else {
			t.Logf("⚠️  'joy' emotion NOT found (might need better detection)")
		}

		// Verifica che sadness venga PRIMA di joy (transizione corretta)
		if hasSadness && hasJoy && sadnessScene < joyScene {
			t.Logf("✅ Emotion transition correct: sadness (scene %d) → joy (scene %d)",
				sadnessScene, joyScene)
		} else if hasSadness && hasJoy {
			t.Errorf("❌ Emotion transition WRONG order: sadness (scene %d) should be before joy (scene %d)",
				sadnessScene, joyScene)
		}
	})

	// --- Verifica 2: Tipo di scena ---
	t.Run("SceneTypes", func(t *testing.T) {
		sceneTypes := []string{}
		for _, scene := range script.Scenes {
			sceneTypes = append(sceneTypes, string(scene.Type))
		}

		t.Logf("Scene types: %v", sceneTypes)

		// Prima scena dovrebbe essere intro/hook
		if script.Scenes[0].Type == SceneIntro || script.Scenes[0].Type == SceneHook {
			t.Logf("✅ First scene is %s (correct)", script.Scenes[0].Type)
		}

		// Ultima scena dovrebbe essere conclusion
		lastScene := script.Scenes[len(script.Scenes)-1]
		if lastScene.Type == SceneConclusion {
			t.Logf("✅ Last scene is conclusion (correct)")
		}
	})

	// --- Verifica 3: Visual Cues ---
	t.Run("VisualCues", func(t *testing.T) {
		allCues := []string{}
		for _, scene := range script.Scenes {
			allCues = append(allCues, scene.VisualCues...)
		}

		t.Logf("Visual cues found: %v", allCues)

		// Dovrebbe trovare almeno un cue visivo (input contiene "mostra")
		if len(allCues) > 0 {
			t.Logf("✅ Found %d visual cues", len(allCues))
		} else {
			t.Logf("⚠️  No visual cues found")
		}
	})

	// --- Verifica 4: Category detection ---
	t.Run("CategoryDetection", func(t *testing.T) {
		t.Logf("Detected category: '%s'", script.Metadata.Category)

		// Con tono "cinematic" e testo emozionale, categoria attesa non è "tech"
		if script.Metadata.Category == "tech" {
			t.Logf("⚠️  Category is 'tech' but input is emotional/cinematic")
		}
	})

	// --- Output JSON completo ---
	t.Logf("\n========== FULL SCRIPT JSON ==========")
	data, _ := json.MarshalIndent(script, "", "  ")
	t.Logf("%s", string(data))
	t.Logf("========================================")
}

// Test 3: Narrativo a Fasi - verifica struttura scene numerate
func TestStress_NarrativePhases(t *testing.T) {
	input := "Guida pratica per creare un brand di successo: " +
		"1. Trova la tua nicchia. " +
		"2. Disegna un logo minimale. " +
		"3. Crea una strategia social aggressiva. " +
		"4. Analizza i dati di vendita. " +
		"Concludi con una call to action per iscriversi al canale."

	title := "How to Build a Successful Brand"
	tone := "educational"
	targetDuration := 180

	parser := NewParser(targetDuration, "italian")
	startTime := time.Now()

	script, err := parser.Parse(input, title, tone, "gemma3:4b")
	if err != nil {
		t.Fatalf("Parser failed: %v", err)
	}

	elapsed := time.Since(startTime)

	// --- Verifica 1: Numero scene ---
	t.Run("SceneCount", func(t *testing.T) {
		t.Logf("Generated %d scene", len(script.Scenes))

		// Ci aspettiamo 5-6 scene (intro + 4 steps + conclusion)
		if len(script.Scenes) >= 3 && len(script.Scenes) <= 8 {
			t.Logf("✅ Scene count in acceptable range: %d (expected 5-6)", len(script.Scenes))
		} else {
			t.Errorf("❌ Scene count out of range: %d (expected 5-6)", len(script.Scenes))
		}
	})

	// --- Verifica 2: Scene numerate correttamente ---
	t.Run("SceneNumbering", func(t *testing.T) {
		for i, scene := range script.Scenes {
			expectedNum := i + 1
			if scene.SceneNumber != expectedNum {
				t.Errorf("Scene %d has wrong number: %d", i, scene.SceneNumber)
			}
		}
		t.Logf("✅ All scenes numbered correctly: 1 to %d", len(script.Scenes))
	})

	// --- Verifica 3: Durata proporzionata ---
	t.Run("DurationDistribution", func(t *testing.T) {
		totalDuration := 0
		for _, scene := range script.Scenes {
			totalDuration += scene.Duration
			t.Logf("Scene %d: %ds (%d words)", scene.SceneNumber, scene.Duration, scene.WordCount)
		}

		t.Logf("Total estimated duration: %ds (target: %ds)", totalDuration, targetDuration)

		if totalDuration > 0 {
			t.Logf("✅ Durations are non-zero")
		} else {
			t.Errorf("❌ All durations are zero")
		}

		// Verifica che le durate siano proporzionate alla lunghezza del testo
		if len(script.Scenes) > 1 {
			// La scena più lunga dovrebbe avere più parole
			for _, scene := range script.Scenes {
				if scene.Duration == 0 && scene.WordCount > 0 {
					t.Logf("⚠️  Scene %d has %ds duration but %d words",
						scene.SceneNumber, scene.Duration, scene.WordCount)
				}
			}

			// La scena più lunga dovrebbe avere più parole
			longestScene := script.Scenes[0]
			for _, scene := range script.Scenes[1:] {
				if scene.Duration > longestScene.Duration {
					longestScene = scene
				}
			}
			t.Logf("Longest scene: #%d (%ds, %d words)",
				longestScene.SceneNumber, longestScene.Duration, longestScene.WordCount)
		}
	})

	// --- Verifica 4: Contenuti delle scene ---
	t.Run("SceneContent", func(t *testing.T) {
		sceneTexts := []string{}
		for _, scene := range script.Scenes {
			sceneTexts = append(sceneTexts, scene.Text)
		}

		t.Logf("Numbered steps found in scenes:")
		for _, scene := range script.Scenes {
			if strings.Contains(scene.Text, "1.") ||
				strings.Contains(scene.Text, "2.") ||
				strings.Contains(scene.Text, "3.") ||
				strings.Contains(scene.Text, "4.") {
				t.Logf("✅ Scene %d contains numbered steps", scene.SceneNumber)
			}
		}

		// Verifica presenza di keywords business/marketing
		allKeywords := map[string]bool{}
		for _, scene := range script.Scenes {
			for _, kw := range scene.Keywords {
				allKeywords[strings.ToLower(kw)] = true
			}
		}

		businessTerms := []string{"brand", "nicchia", "logo", "social", "strategia", "vendite"}
		foundCount := 0
		for _, term := range businessTerms {
			if allKeywords[term] {
				foundCount++
				t.Logf("✅ Found business term: '%s'", term)
			}
		}
		t.Logf("Business terms found: %d/%d", foundCount, len(businessTerms))
	})

	// --- Verifica 5: Performance ---
	t.Run("Performance", func(t *testing.T) {
		if elapsed > 5*time.Second {
			t.Errorf("❌ Parser took %v (expected < 5s)", elapsed)
		} else {
			t.Logf("✅ Parser completed in %v", elapsed)
		}
	})

	// --- Output JSON completo ---
	t.Logf("\n========== FULL SCRIPT JSON ==========")
	data, _ := json.MarshalIndent(script, "", "  ")
	t.Logf("%s", string(data))
	t.Logf("========================================")
}

// ============================================================================
// HELPER: Stampa risultati in formato leggibile
// ============================================================================

func printTestHeader(n int, title string) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 80))
	fmt.Printf("TEST %d: %s\n", n, title)
	fmt.Printf("%s\n", strings.Repeat("=", 80))
}
