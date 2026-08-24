package local

import (
	"context"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestExtractorNASAGenesisMissionGolden(t *testing.T) {
	text := "NASA supports the Genesis Mission. President Donald Trump announced that the United States will participate. The White House Office of Science and Technology Policy coordinates the initiative for Earth research. NASA and the Genesis Mission will continue the work."
	result, err := NewExtractor().ExtractEntities(context.Background(), scriptpkg.EntityExtractionRequest{
		Text: text, Language: "en", EntityCount: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	requireEntity := func(value, kind string) {
		t.Helper()
		for _, group := range [][]scriptpkg.Entity{result.Persons, result.Places, result.Concepts} {
			for _, entity := range group {
				if strings.EqualFold(entity.Value, value) && entity.Type == kind {
					return
				}
			}
		}
		t.Fatalf("missing %s %q: persons=%+v places=%+v concepts=%+v", kind, value, result.Persons, result.Places, result.Concepts)
	}
	requireEntity("NASA", "ORG")
	requireEntity("Donald Trump", "PERSON")
	requireEntity("United States", "GPE")
	requireEntity("White House Office of Science and Technology Policy", "ORG")
	requireEntity("Genesis Mission", "CONCEPT")
	requireEntity("Earth", "CONCEPT")

	for _, group := range [][]scriptpkg.Entity{result.Persons, result.Places, result.Concepts} {
		for _, entity := range group {
			if entity.Value == "Genesis" || entity.Value == "United" || entity.Value == "States" || entity.Value == "White" {
				t.Fatalf("fragmented entity leaked: %+v", entity)
			}
		}
	}
}

func TestEntitySpansLongestSpanWins(t *testing.T) {
	text := "White House Office of Science and Technology Policy advised the United States."
	spans := entitySpans(text)
	values := make([]string, 0, len(spans))
	for _, span := range spans {
		values = append(values, text[span[0]:span[1]])
	}
	want := []string{"White House Office of Science and Technology Policy", "United States"}
	if len(values) != len(want) {
		t.Fatalf("spans=%v, want %v", values, want)
	}
	for i := range want {
		if values[i] != want[i] {
			t.Fatalf("spans=%v, want %v", values, want)
		}
	}
}
