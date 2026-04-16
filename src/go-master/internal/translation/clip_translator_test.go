package translation

import (
	"strings"
	"testing"
)

// TestTranslator_ITtoEN verifica che le keywords italiane vengano tradotte correttamente
func TestTranslator_ITtoEN(t *testing.T) {
	translator := NewClipSearchTranslator()

	tests := []struct {
		name     string
		input    []string
		expected []string // keywords che DEVONO essere presenti (in inglese)
	}{
		{
			name:  "Technical/Quantum Computing",
			input: []string{"calcolo", "quantistico", "qubit", "silicio", "server", "farm", "raffreddate", "simulazioni", "laboratori"},
			expected: []string{"quantum", "silicon", "server", "farm", "cooled", "simulation", "laboratory"},
		},
		{
			name:  "Emotional/Cinematic",
			input: []string{"malinconico", "solitudine", "pioggia", "alba", "vuoto", "città", "strade"},
			expected: []string{"melancholic", "loneliness", "rain", "dawn", "empty", "city", "streets"},
		},
		{
			name:  "Energy/Joy",
			input: []string{"energia", "sorriso", "correre", "sole", "solare", "motivazione", "persone"},
			expected: []string{"energy", "smile", "running", "sun", "sunny", "motivation", "people"},
		},
		{
			name:  "Business/Brand",
			input: []string{"brand", "nicchia", "logo", "strategia", "vendite", "successo", "lavorare"},
			expected: []string{"brand", "niche", "logo", "strategy", "sales", "success", "working"},
		},
		{
			name:  "Mixed IT/EN (should not double-translate)",
			input: []string{"technology", "computer", "ai", "robot", "digital"},
			expected: []string{"technology", "computer", "ai", "robot", "digital"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			translated := translator.TranslateKeywords(tt.input)

			t.Logf("Input:    %v", tt.input)
			t.Logf("Output:   %v", translated)

			// Verifica che tutte le keywords attese siano presenti
			foundCount := 0
			for _, exp := range tt.expected {
				found := false
				for _, out := range translated {
					if strings.Contains(strings.ToLower(out), strings.ToLower(exp)) {
						found = true
						break
					}
				}
				if found {
					foundCount++
					t.Logf("  ✅ Found expected: '%s'", exp)
				} else {
					t.Logf("  ❌ Missing expected: '%s'", exp)
				}
			}

			coverage := float64(foundCount) / float64(len(tt.expected)) * 100
			t.Logf("Translation coverage: %.0f%% (%d/%d)", coverage, foundCount, len(tt.expected))

			if coverage < 80 {
				t.Errorf("Translation coverage too low: %.0f%% (expected >= 80%%)", coverage)
			}
		})
	}
}

// TestTranslator_Emotions verifica traduzione emozioni
func TestTranslator_Emotions(t *testing.T) {
	translator := NewClipSearchTranslator()

	tests := []struct {
		input    []string
		expected []string
	}{
		{[]string{"tristezza", "malinconia"}, []string{"sadness"}},
		{[]string{"gioia", "felicità"}, []string{"joy"}},
		{[]string{"energia"}, []string{"energy"}},
		{[]string{"paura"}, []string{"fear"}},
		{[]string{"sorpresa"}, []string{"surprise"}},
	}

	for _, tt := range tests {
		t.Run(tt.input[0], func(t *testing.T) {
			translated := translator.TranslateEmotions(tt.input)
			t.Logf("Input: %v → Output: %v", tt.input, translated)

			for _, exp := range tt.expected {
				found := false
				for _, out := range translated {
					if strings.EqualFold(out, exp) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected emotion '%s' not found in %v", exp, translated)
				}
			}
		})
	}
}

// TestTranslator_QueryTranslation verifica traduzione query intere
func TestTranslator_QueryTranslation(t *testing.T) {
	translator := NewClipSearchTranslator()

	tests := []struct {
		input    string
		contains string // la query tradotta deve contenere questa parola
	}{
		{"calcolo quantistico qubit", "quantum"},
		{"server farm raffreddate liquido", "server"},
		{"malinconico solitudine pioggia", "melancholic"},
		{"strategia social vendita", "strategy"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			translated := translator.TranslateQuery(tt.input)
			t.Logf("Input: '%s' → Output: '%s'", tt.input, translated)

			if !strings.Contains(strings.ToLower(translated), strings.ToLower(tt.contains)) {
				t.Errorf("Translated query '%s' does not contain '%s'", translated, tt.contains)
			}
		})
	}
}

// TestTranslator_Comprehensive verifica dizionario completo
func TestTranslator_Comprehensive(t *testing.T) {
	translator := NewClipSearchTranslator()

	// Conta entries nel dizionario
	dict := buildClipDictionary()
	t.Logf("Dictionary size: %d entries", len(dict))

	if len(dict) < 100 {
		t.Errorf("Dictionary too small: %d entries (expected >= 100)", len(dict))
	}

	// Verifica categories
	categories := map[string]int{
		"tech":     0,
		"emotion":  0,
		"business": 0,
		"visual":   0,
		"general":  0,
	}

	for it, en := range dict {
		itLower := strings.ToLower(it)
		// Categorizza in base al contenuto
		if strings.Contains(itLower, "calcolo") || strings.Contains(itLower, "tech") || strings.Contains(itLower, "server") {
			categories["tech"]++
		}
		if strings.Contains(itLower, "felice") || strings.Contains(itLower, "triste") || strings.Contains(itLower, "energia") {
			categories["emotion"]++
		}
		if strings.Contains(itLower, "business") || strings.Contains(itLower, "vendita") || strings.Contains(itLower, "strategia") {
			categories["business"]++
		}
		_ = en
	}

	t.Logf("Category coverage: %+v", categories)

	// Verifica alcune traduzioni critiche
	criticalTranslations := map[string]string{
		"quantistico":  "quantum",
		"qubit":        "qubit",
		"silicio":      "silicon",
		"malinconico":  "melancholic",
		"solitudine":   "loneliness",
		"sorriso":      "smile",
		"strategia":    "strategy",
		"vendite":      "sales",
		"laboratorio":  "laboratory",
		"simulazione":  "simulation",
	}

	t.Log("Critical translations check:")
	for it, expectedEN := range criticalTranslations {
		actual := translator.TranslateKeywords([]string{it})
		if len(actual) > 0 && strings.EqualFold(actual[0], expectedEN) {
			t.Logf("  ✅ '%s' → '%s'", it, actual[0])
		} else {
			t.Errorf("  ❌ '%s' → expected '%s', got '%v'", it, expectedEN, actual)
		}
	}
}
