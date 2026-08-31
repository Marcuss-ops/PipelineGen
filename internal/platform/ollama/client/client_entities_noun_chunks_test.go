package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

func TestParseEntityExtractionResultPreservesGroundedNounChunksAcrossLanguages(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		chunks []string
	}{
		{"it", "L'astice blu vive nei mari bretoni.", []string{"astice blu", "mari bretoni"}},
		{"en", "A massive blue whale swims through the cold Atlantic Ocean.", []string{"massive blue whale", "cold Atlantic Ocean"}},
		{"es", "El enorme tiburón blanco nada cerca de una pequeña embarcación pesquera.", []string{"enorme tiburón blanco", "pequeña embarcación pesquera"}},
		{"pt", "Os pescadores brasileiros navegam em pequenos barcos de madeira.", []string{"pescadores brasileiros", "pequenos barcos de madeira"}},
		{"fr", "L'ancienne cathédrale domine le centre historique.", []string{"ancienne cathédrale", "centre historique"}},
		{"de", "Ein moderner Hochgeschwindigkeitszug fährt durch die Schweizer Alpen.", []string{"moderner Hochgeschwindigkeitszug", "Schweizer Alpen"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := `{"noun_chunks": [` + quoteJSON(tc.chunks) + `], "artlist_phrases": []}`
			result, err := parseEntityExtractionResult(response, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.NounChunks) != len(tc.chunks) {
				t.Fatalf("noun_chunks = %v, want %v", result.NounChunks, tc.chunks)
			}
			for i := range tc.chunks {
				if result.NounChunks[i] != tc.chunks[i] {
					t.Fatalf("noun_chunks[%d] = %q, want %q", i, result.NounChunks[i], tc.chunks[i])
				}
			}
			if !containsAllSourceChunks(tc.text, result.NounChunks) {
				t.Fatalf("parser returned non-grounded noun_chunks = %v", result.NounChunks)
			}
		})
	}
}

func TestParsePlainTextEntityResultPreservesNounChunks(t *testing.T) {
	input := `
## frasi_importanti
- L'astice blu vive nei mari bretoni.
## entity_senza_testo
## nomi_speciali
## parole_importanti
- astice
## artlist_phrases
- seafood restaurant
## noun_chunks
- astice blu
- mari bretoni
- frutti di mare
`
	got, err := parsePlainTextEntityResult(input, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"astice blu", "mari bretoni", "frutti di mare"}
	if !sameStrings(got.NounChunks, want) {
		t.Fatalf("noun_chunks = %#v, want %#v", got.NounChunks, want)
	}
}

func TestResultIsEmptyNounChunksCountAsContent(t *testing.T) {
	if resultIsEmpty(&detail.EntityExtractionResult{NounChunks: []string{"blue whale"}}) {
		t.Fatal("result containing noun_chunks must not be considered empty")
	}
}

func TestNounChunkGoldenCorpusWithOllama(t *testing.T) {
	if os.Getenv("RUN_OLLAMA_GOLDEN") != "1" {
		t.Skip("set RUN_OLLAMA_GOLDEN=1 to run the live multilingual model evaluation")
	}
	endpoint := os.Getenv("OLLAMA_URL")
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}
	model := os.Getenv("OLLAMA_ENTITY_MODEL")
	if model == "" {
		model = "gemma4:e2b"
	}
	corpus, err := os.ReadFile(filepath.Join("testdata", "noun_chunks_multilingual_extended.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name      string   `json:"name"`
		Language  string   `json:"language"`
		Text      string   `json:"text"`
		Required  []string `json:"required"`
		Forbidden []string `json:"forbidden"`
	}
	if err := json.Unmarshal(corpus, &cases); err != nil {
		t.Fatal(err)
	}
	var totalRequired, foundRequired int
	var totalForbidden, foundForbidden int
	var totalOutput, groundedOutput, duplicateOutput int
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			result, err := NewClient(endpoint, model, 120).ExtractEntitiesFromSegmentWithModel(t.Context(), detail.EntityExtractionRequest{
				SegmentText: tc.Text, Language: tc.Language, EntityCount: 8,
			}, model)
			if err != nil {
				t.Fatal(err)
			}
			totalOutput += len(result.NounChunks)
			for _, chunk := range result.NounChunks {
				if strings.Contains(strings.ToLower(tc.Text), strings.ToLower(chunk)) {
					groundedOutput++
				}
			}
			duplicateOutput += duplicateCount(result.NounChunks)
			for _, required := range tc.Required {
				totalRequired++
				if !containsFold(result.NounChunks, required) {
					t.Errorf("noun_chunks=%v, missing required grounded chunk %q", result.NounChunks, required)
				} else {
					foundRequired++
				}
			}
			for _, forbidden := range tc.Forbidden {
				totalForbidden++
				if containsFold(result.NounChunks, forbidden) {
					foundForbidden++
					t.Errorf("noun_chunks=%v, contains forbidden narrative item %q", result.NounChunks, forbidden)
				}
			}
		})
	}
	t.Logf("metrics: required_chunk_recall=%.2f%% forbidden_phrase_rate=%.2f%% grounding_accuracy=%.2f%% duplicate_rate=%.2f%%",
		percent(foundRequired, totalRequired), percent(foundForbidden, totalForbidden), percent(groundedOutput, totalOutput), percent(duplicateOutput, totalOutput))
}

func TestSanitizeNounChunksRejectsHallucinatedDetailsAndFunctionLeads(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	var profile *linguistics.LexiconProfile
	for dir := filepath.Dir(filename); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		root := filepath.Join(dir, "config", "lexicons")
		if _, err := os.Stat(root); err != nil {
			continue
		}
		registry, err := linguistics.NewLexiconRegistry(root)
		if err != nil {
			t.Fatal(err)
		}
		profile = registry.Resolve("en")
		break
	}
	text := "The man entered the room and sat quietly."
	result := &detail.EntityExtractionResult{NounChunks: []string{
		"man", "room", "dark room", "wooden chair", "the room", "mysterious man",
	}}
	filtered := result
	filtered.NounChunks = filterGroundedNounChunks(text, result.NounChunks, profile)
	want := []string{"man", "room"}
	if len(filtered.NounChunks) != len(want) {
		t.Fatalf("grounded noun_chunks = %v, want only source spans", filtered.NounChunks)
	}
	for _, forbidden := range []string{"mysterious man", "the room"} {
		for _, got := range filtered.NounChunks {
			if strings.EqualFold(got, forbidden) {
				t.Fatalf("invalid noun chunk survived: %q", got)
			}
		}
	}
}

func quoteJSON(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return strings.Join(quoted, ",")
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsFold(values []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == want || strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func containsAllSourceChunks(text string, chunks []string) bool {
	for _, chunk := range chunks {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(chunk)) {
			return false
		}
	}
	return true
}

func duplicateCount(values []string) int {
	seen := make(map[string]struct{}, len(values))
	duplicates := 0
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if _, ok := seen[key]; ok {
			duplicates++
			continue
		}
		seen[key] = struct{}{}
	}
	return duplicates
}

func percent(value, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(value) * 100 / float64(total)
}
