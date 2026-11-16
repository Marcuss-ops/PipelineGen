package rustexec

import (
	"context"
	"strings"
	"testing"

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
