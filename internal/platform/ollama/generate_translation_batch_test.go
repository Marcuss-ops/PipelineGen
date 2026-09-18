package ollama

import (
	"errors"
	"strings"
	"testing"
)

func batchSegments(ids ...string) []BatchTranslationSegment {
	segments := make([]BatchTranslationSegment, 0, len(ids))
	for _, id := range ids {
		segments = append(segments, BatchTranslationSegment{ID: id, Text: "text " + id})
	}
	return segments
}

func TestParseBatchTranslationResponse_Valid(t *testing.T) {
	chunk := batchSegments("0", "1")
	raw := `{"translations":[{"id":"1","text":"due"},{"id":"0","text":"uno"}]}`
	got, err := parseBatchTranslationResponse(raw, chunk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Answer order is NOT trusted: the ids decide the attribution.
	if got["0"] != "uno" || got["1"] != "due" {
		t.Fatalf("attribution wrong: %+v", got)
	}
}

func TestParseBatchTranslationResponse_FencedJSON(t *testing.T) {
	chunk := batchSegments("0")
	raw := "```json\n{\"translations\":[{\"id\":\"0\",\"text\":\"ciao\"}]}\n```"
	got, err := parseBatchTranslationResponse(raw, chunk)
	if err != nil {
		t.Fatalf("fenced payload must be accepted: %v", err)
	}
	if got["0"] != "ciao" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseBatchTranslationResponse_ContractViolations(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		chunk []BatchTranslationSegment
	}{
		{name: "empty", raw: "   ", chunk: batchSegments("0")},
		{name: "unparsable", raw: "sure, here you go: uno e due", chunk: batchSegments("0")},
		{name: "missing id", raw: `{"translations":[{"id":"0","text":"uno"}]}`, chunk: batchSegments("0", "1")},
		{name: "unknown id", raw: `{"translations":[{"id":"9","text":"x"},{"id":"0","text":"uno"}]}`, chunk: batchSegments("0")},
		{name: "duplicate id", raw: `{"translations":[{"id":"0","text":"uno"},{"id":"0","text":"due"}]}`, chunk: batchSegments("0")},
		{name: "empty text", raw: `{"translations":[{"id":"0","text":"  "}]}`, chunk: batchSegments("0")},
		{name: "extra id", raw: `{"translations":[{"id":"0","text":"uno"},{"id":"1","text":"due"}]}`, chunk: batchSegments("0")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseBatchTranslationResponse(tc.raw, tc.chunk)
			if err == nil {
				t.Fatal("expected a contract violation, got nil")
			}
			if !errors.Is(err, ErrBatchTranslationContract) {
				t.Fatalf("expected ErrBatchTranslationContract, got: %v", err)
			}
		})
	}
}

func TestBatchTranslationPrompts_CarriesEveryIDInOrder(t *testing.T) {
	chunk := batchSegments("3", "4")
	system, user := batchTranslationPrompts(chunk, "Italian")
	if !strings.Contains(system, "professional translator") {
		t.Fatalf("system prompt lost the translator persona: %q", system)
	}
	if !strings.Contains(user, "Italian") {
		t.Fatalf("user prompt must name the target language: %q", user)
	}
	if !strings.Contains(user, `{"translations":[{"id":"<id>","text":"<translated text>"}]}`) {
		t.Fatalf("user prompt must state the JSON contract: %q", user)
	}
	first := strings.Index(user, "[3] text 3")
	second := strings.Index(user, "[4] text 4")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("segments must be listed with their ids in order: %q", user)
	}
}
