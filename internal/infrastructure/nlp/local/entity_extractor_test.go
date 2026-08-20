package local

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/linguistics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type deviceTestExtractor struct {
	result *scriptpkg.EntityResult
	calls  int
}

func (e *deviceTestExtractor) ExtractEntities(context.Context, scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	e.calls++
	return e.result, nil
}

func TestMain(m *testing.M) {
	wd, _ := os.Getwd()
	for dir := wd; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		root := filepath.Join(dir, "config", "lexicons")
		if _, err := os.Stat(root); err == nil {
			if registry, err := linguistics.NewLexiconRegistry(root); err == nil {
				_ = linguistics.SetDefaultLexicon(registry)
			}
			break
		}
	}
	os.Exit(m.Run())
}

func extract(t *testing.T, text string) *scriptpkg.EntityResult {
	t.Helper()
	result, err := NewExtractor().ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{Text: text, Language: "it", EntityCount: 8})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestExtractorItalianSceneIsLocalAndDeterministic(t *testing.T) {
	text := "Nel 1986 Mike Tyson conquistò il titolo WBC. A Las Vegas diventò un campione discusso."
	first := extract(t, text)
	second := extract(t, text)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatalf("extractor is not deterministic:\n%s\n%s", a, b)
	}
	if len(first.ImportantPhrases) != 1 {
		t.Fatalf("important phrases = %d, want 1", len(first.ImportantPhrases))
	}
	if len(first.Persons) == 0 || first.Persons[0].Value != "Mike Tyson" {
		t.Fatalf("persons = %+v", first.Persons)
	}
	if len(first.Places) < 2 {
		t.Fatalf("places/orgs = %+v", first.Places)
	}
	if len(first.ImportantWords) == 0 {
		t.Fatal("important words are empty")
	}
}

func TestExtractorDoesNotInventEntitiesOrStopwords(t *testing.T) {
	result := extract(t, "Mike Tyson allenava potenza e velocità.")
	for _, entity := range result.Persons {
		if entity.Value == "Muhammad Ali" || entity.Value == "Las Vegas" {
			t.Fatalf("invented entity: %+v", entity)
		}
	}
	for _, word := range result.ImportantWords {
		if word == "della" || word == "questo" || word == "anche" || word == "con" || word == "per" || word == "sono" {
			t.Fatalf("stopword returned as important word: %q", word)
		}
	}
}

func TestExtractorSupportsUnicodeInput(t *testing.T) {
	result := extract(t, "L’ascesa di Muhammad Ali cambiò la percezione del pugilato.")
	if len(result.ImportantPhrases) != 1 {
		t.Fatalf("phrases = %+v", result.ImportantPhrases)
	}
	if result.ImportantPhrases[0] == "" {
		t.Fatal("empty phrase")
	}
}

func TestExtractorRanksNarrativeSentenceAndConcreteKeywords(t *testing.T) {
	text := "La boxe ha una storia lunga e complessa. Nel 1986 Mike Tyson conquistò il titolo mondiale e cambiò per sempre la percezione del pugilato. Gli allenatori studiarono a lungo quella trasformazione tecnica."
	result := extract(t, text)

	if len(result.ImportantPhrases) != 1 {
		t.Fatalf("phrases = %+v, want exactly one source sentence", result.ImportantPhrases)
	}
	if result.ImportantPhrases[0] != "Nel 1986 Mike Tyson conquistò il titolo mondiale e cambiò per sempre la percezione del pugilato." {
		t.Fatalf("phrase = %q, want the entity-and-year narrative sentence", result.ImportantPhrases[0])
	}

	for _, word := range result.ImportantWords {
		if word == "della" || word == "quella" || word == "per" || word == "una" || word == "nel" {
			t.Fatalf("function word returned as important: %q (all=%v)", word, result.ImportantWords)
		}
		if !strings.Contains(strings.ToLower(text), strings.ToLower(word)) {
			t.Fatalf("keyword %q was not found in source text", word)
		}
	}
	if len(result.ImportantWords) == 0 {
		t.Fatal("concrete important words are empty")
	}
}

// TestExtractorGolden02RejectsEnglishStopwords certifies the GOLDEN 02
// stop-word/function-word rejection on the English boundary: the
// deterministic CPU-only path must never surface a stop word or function
// word as an important word, must classify person names with the canonical
// PERSON type, and must keep concrete entity-adjacent words. Every surfaced
// word must occur verbatim in the source text.
//
// Boundary note: the local extractor is deliberately conservative. Single-word
// companies ("Tesla"), currency-denominated money amounts ("$10 billion") and
// generic-verb rejection are the LLM/VidRush extraction path's responsibility,
// not this CPU-only fallback. Spoken cardinal figures ("ten billion") ARE now
// owned here deterministically (see TestExtractorCardinalNumberPhrases). This
// test certifies the boundary this package DOES own: stop-word and
// function-word rejection via the LexiconRegistry SSOT.
func TestExtractorGolden02RejectsEnglishStopwords(t *testing.T) {
	text := "Elon Musk announced that Tesla will invest ten billion dollars in artificial intelligence."
	first, err := NewExtractor().ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{
		Text: text, Language: "en", EntityCount: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Determinism: same input, same output (byte-identical).
	second, _ := NewExtractor().ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{
		Text: text, Language: "en", EntityCount: 8,
	})
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("extractor is not deterministic for English input")
	}

	// Person classified with the canonical type.
	if len(first.Persons) == 0 || first.Persons[0].Value != "Elon Musk" || first.Persons[0].Type != "PERSON" {
		t.Fatalf("persons = %+v, want Elon Musk (PERSON)", first.Persons)
	}

	// Stop words and function words must never surface as important words.
	stop := map[string]bool{
		"that": true, "will": true, "the": true, "in": true, "and": true, "a": true, "ten": true,
	}
	for _, word := range first.ImportantWords {
		if stop[word] {
			t.Fatalf("stop/function word surfaced as important word: %q (all=%v)", word, first.ImportantWords)
		}
		if !strings.Contains(strings.ToLower(text), word) {
			t.Fatalf("important word %q not found verbatim in source", word)
		}
	}
	if len(first.ImportantWords) == 0 {
		t.Fatal("important words are empty")
	}
	// Concrete entity-adjacent words survive the stop-word filter.
	for _, want := range []string{"artificial", "intelligence"} {
		found := false
		for _, word := range first.ImportantWords {
			if word == want {
				found = true
			}
		}
		if !found {
			t.Errorf("concrete keyword %q missing from important words: %v", want, first.ImportantWords)
		}
	}
}

// TestExtractorCardinalNumberPhrases certifies the deterministic CARDINAL
// mapping for spoken figures: "ten million" must surface as a single
// CARDINAL entity (never two KEYWORD fragments), which the overlay chain then
// maps CARDINAL → KindNumber → NUMBER template. This is the model-free NLP
// source for the E2E "NUMBER" gate; a bare number word ("ten", "one") is
// deliberately left unmatched because it is too ambiguous on its own.
func TestExtractorCardinalNumberPhrases(t *testing.T) {
	text := "Michael Jordan signed a major partnership with Nike. The company sold ten million products in Chicago."
	result, err := NewExtractor().ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{
		Text: text, Language: "en", EntityCount: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	requireCardinal := func(value string) {
		t.Helper()
		for _, concept := range result.Concepts {
			if concept.Type == "CARDINAL" && strings.EqualFold(strings.TrimSpace(concept.Value), value) {
				return
			}
		}
		t.Fatalf("missing CARDINAL %q: concepts=%+v", value, result.Concepts)
	}
	requireCardinal("ten million")

	// A bare number word is too ambiguous to become a CARDINAL overlay.
	for _, concept := range result.Concepts {
		if concept.Type == "CARDINAL" && strings.EqualFold(strings.TrimSpace(concept.Value), "ten") {
			t.Fatalf("bare number word promoted to CARDINAL: %+v", concept)
		}
	}
}

func TestHybridExtractorRoutesExplicitDevices(t *testing.T) {
	cpu := &deviceTestExtractor{result: &scriptpkg.EntityResult{ImportantPhrases: []string{"cpu"}}}
	gpu := &deviceTestExtractor{result: &scriptpkg.EntityResult{ImportantPhrases: []string{"gpu"}}}
	hybrid := &HybridExtractor{cpu: cpu, gpu: gpu, available: func(context.Context) bool { return true }}

	got, err := hybrid.ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{Text: "x", Device: DeviceGPU})
	if err != nil || got.ImportantPhrases[0] != "gpu" || gpu.calls != 1 {
		t.Fatalf("gpu route = %+v, err=%v, calls=%d", got, err, gpu.calls)
	}
	got, err = hybrid.ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{Text: "x", Device: DeviceCPU})
	if err != nil || got.ImportantPhrases[0] != "cpu" || cpu.calls != 1 {
		t.Fatalf("cpu route = %+v, err=%v, calls=%d", got, err, cpu.calls)
	}
}

func TestHybridExtractorGPUFailsClosedWhenUnavailable(t *testing.T) {
	hybrid := &HybridExtractor{
		cpu:       NewExtractor(),
		gpu:       &deviceTestExtractor{result: &scriptpkg.EntityResult{}},
		available: func(context.Context) bool { return false },
	}
	_, err := hybrid.ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{Text: "x", Device: DeviceGPU})
	if !errors.Is(err, scriptpkg.ErrEntityExtractorUnavailable) {
		t.Fatalf("error = %v, want ErrEntityExtractorUnavailable", err)
	}
	got, err := hybrid.ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{Text: "Mike Tyson", Device: DeviceAuto})
	if err != nil || got == nil {
		t.Fatalf("auto fallback = %+v, err=%v", got, err)
	}
}

// TestExtractorRejectsSentenceInitialConnectivesAsEntities certifies the
// false-positive fix: a sentence-initial capitalized connective like
// "During the" or "Shortly the" must never be emitted as a named entity. The
// capitalization heuristic alone would classify them as PERSON; the canonical
// entity blocklist (config/lexicons/en/entity_blocklist.txt) must reject them
// while the real entities in the same text still survive.
func TestExtractorRejectsSentenceInitialConnectivesAsEntities(t *testing.T) {
	text := "During the dawn of civilization, the Genesis Mission began. Shortly the probe returned solar wind samples to NASA."
	result, err := NewExtractor().ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{
		Text: text, Language: "en", EntityCount: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, group := range [][]scriptpkg.Entity{result.Persons, result.Places, result.Concepts} {
		for _, entity := range group {
			// KEYWORDs are important words (search terms), not named entities;
			// only the named-entity classifications are in scope for this fix.
			if entity.Type == "KEYWORD" {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(entity.Value)) {
			case "during the", "shortly the", "during", "shortly":
				t.Fatalf("sentence-initial connective leaked as named entity: %+v (persons=%+v places=%+v concepts=%+v)",
					entity, result.Persons, result.Places, result.Concepts)
			}
		}
	}

	requireEntity := func(value, kind string) {
		t.Helper()
		for _, group := range [][]scriptpkg.Entity{result.Persons, result.Places, result.Concepts} {
			for _, entity := range group {
				if strings.EqualFold(strings.TrimSpace(entity.Value), value) && entity.Type == kind {
					return
				}
			}
		}
		t.Fatalf("missing %s %q: persons=%+v places=%+v concepts=%+v", kind, value, result.Persons, result.Places, result.Concepts)
	}
	requireEntity("Genesis Mission", "CONCEPT")
	requireEntity("NASA", "ORG")
}
