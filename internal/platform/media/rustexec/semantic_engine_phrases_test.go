package rustexec

import (
	"context"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func TestSelectPhrasesAcceptsGroundedRustSpans(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ok":true,"phrases":[{"text":"built props","start_byte":13,"end_byte":24,"start_rune":13,"end_rune":24,"score":9}]}`)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	phrases, err := adapter.SelectPhrases(context.Background(), scriptgen.PhraseSelectionRequest{
		Text: "Ada Lovelace built props.",
		Profile: &scriptgen.PhraseSelectionProfile{
			StopWords: []string{"they"}, FunctionWords: []string{"they"},
			MinWords: 2, MaxWords: 16, MaxResults: 5, RejectVerbsWhenAll: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(phrases) != 1 || phrases[0].Text != "built props" {
		t.Fatalf("phrases = %+v", phrases)
	}
	if !strings.Contains(string(runner.input), `"operation":"select_phrases"`) {
		t.Fatalf("request did not carry select_phrases: %s", runner.input)
	}
}

func TestSelectPhrasesRejectsUngroundedRustSpan(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ok":true,"phrases":[{"text":"invented","start_byte":13,"end_byte":24,"start_rune":13,"end_rune":24,"score":9}]}`)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.SelectPhrases(context.Background(), scriptgen.PhraseSelectionRequest{Text: "Ada Lovelace built props."}); err == nil {
		t.Fatal("expected ungrounded phrase to fail closed")
	}
}

func TestAlignPhrasesAllowMissingSkipsUnknownAndKeepsBatch(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ok":true,"alignments":[{"text":"la pena","occurrence":1,"word_start":0,"word_end":1,"start_us":0,"end_us":200}]}`)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	alignments, err := adapter.AlignPhrases(context.Background(), scriptgen.PhraseAlignmentRequest{
		Timing: capabilityaudio.SpeechTimingArtifact{
			Version: capabilityaudio.SpeechTimingVersion, BoundaryMode: capabilityaudio.BoundaryWord,
			TextSHA256: "text", AudioSHA256: "audio", DurationUS: 300,
			Words: []capabilityaudio.SpeechWordTiming{
				{Index: 0, Text: "La", StartUS: 0, EndUS: 100},
				{Index: 1, Text: "pena", StartUS: 100, EndUS: 200},
				{Index: 2, Text: "divenne", StartUS: 200, EndUS: 300},
			},
		},
		Phrases: []string{"la pena", "assente"}, AllowMissing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(alignments) != 1 || alignments[0].Text != "la pena" {
		t.Fatalf("alignments = %+v", alignments)
	}
	if !strings.Contains(string(runner.input), `"allow_missing":true`) {
		t.Fatalf("request did not carry allow_missing: %s", runner.input)
	}
}

func TestAlignPhrasesAllOccurrencesReturnsEveryMatch(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ok":true,"alignments":[{"text":"Garibaldi","occurrence":1,"word_start":0,"word_end":0,"start_us":0,"end_us":100},{"text":"Garibaldi","occurrence":2,"word_start":2,"word_end":2,"start_us":200,"end_us":300}]}`)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	alignments, err := adapter.AlignPhrases(context.Background(), scriptgen.PhraseAlignmentRequest{
		Timing: capabilityaudio.SpeechTimingArtifact{
			Version: capabilityaudio.SpeechTimingVersion, BoundaryMode: capabilityaudio.BoundaryWord,
			TextSHA256: "text", AudioSHA256: "audio", DurationUS: 300,
			Words: []capabilityaudio.SpeechWordTiming{
				{Index: 0, Text: "Garibaldi", StartUS: 0, EndUS: 100},
				{Index: 1, Text: "guidò", StartUS: 100, EndUS: 200},
				{Index: 2, Text: "Garibaldi", StartUS: 200, EndUS: 300},
			},
		},
		Phrases: []string{"Garibaldi"}, AllOccurrences: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(alignments) != 2 || alignments[1].Occurrence != 2 {
		t.Fatalf("alignments = %+v", alignments)
	}
	if !strings.Contains(string(runner.input), `"all_occurrences":true`) {
		t.Fatalf("request did not carry all_occurrences: %s", runner.input)
	}
}

func TestSelectPhrasesRejectsWorkerError(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ok":false,"error":"bad profile"}`)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.SelectPhrases(context.Background(), scriptgen.PhraseSelectionRequest{Text: "Ada Lovelace built props."}); err == nil {
		t.Fatal("expected worker error to fail closed")
	}
}
