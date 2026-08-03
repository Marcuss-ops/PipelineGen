package local

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
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
	if !errors.Is(err, adapters.ErrEntityExtractorUnavailable) {
		t.Fatalf("error = %v, want ErrEntityExtractorUnavailable", err)
	}
	got, err := hybrid.ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{Text: "Mike Tyson", Device: DeviceAuto})
	if err != nil || got == nil {
		t.Fatalf("auto fallback = %+v, err=%v", got, err)
	}
}
