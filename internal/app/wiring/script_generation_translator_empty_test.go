package wiring

import (
	"context"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
)

// stubTranslationPort answers with a fixed result so the adapter's own
// validation (not the provider's) is what the test observes.
type stubTranslationPort struct {
	text     string
	provider string
	err      error
}

func (s *stubTranslationPort) Translate(_ context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	if s.err != nil {
		return translation.TranslationResult{}, s.err
	}
	return translation.TranslationResult{
		TranslatedText: s.text,
		UsedProvider:   s.provider,
		SourceLang:     cmd.SourceLang,
		TargetLang:     cmd.TargetLang,
	}, nil
}

// TestScriptGenerationTranslatorRejectsEmptyText is the guard for the whole
// scene-narration translation path: the runner and the scene-ready coordinator
// both trust this adapter to turn a blank provider answer into an error. If
// this validation is ever dropped, an empty translation is checkpointed as a
// successful scene and every downstream language inherits an empty narration —
// the silent-fake-success shape this contract exists to prevent.
func TestScriptGenerationTranslatorRejectsEmptyText(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "empty", text: ""},
		{name: "whitespace only", text: "   \n\t "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &scriptGenerationTranslator{port: &stubTranslationPort{text: tc.text, provider: "ollama"}}
			got, err := adapter.Translate(context.Background(), scriptgen.TranslationInput{
				SceneID:        "scene-1",
				SourceLanguage: scriptgen.Language("en"),
				TargetLanguage: scriptgen.Language("it"),
				SourceText:     "hello",
			})
			if err == nil {
				t.Fatalf("Translate returned %q with a nil error, want a failure", got)
			}
			if !strings.Contains(err.Error(), "empty text") {
				t.Fatalf("error = %v, want it to name the empty text", err)
			}
			if got != "" {
				t.Fatalf("text = %q, want empty on failure", got)
			}
		})
	}
}

// TestScriptGenerationTranslatorPassesThroughRealText pins the happy path and
// the language threading, so the validation cannot start rejecting valid text.
func TestScriptGenerationTranslatorPassesThroughRealText(t *testing.T) {
	port := &stubTranslationPort{text: "  ciao  ", provider: "argos"}
	adapter := &scriptGenerationTranslator{port: port}

	got, err := adapter.Translate(context.Background(), scriptgen.TranslationInput{
		SceneID:        "scene-1",
		SourceLanguage: scriptgen.Language("en"),
		TargetLanguage: scriptgen.Language("it"),
		SourceText:     "hello",
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if got != "  ciao  " {
		t.Fatalf("text = %q, want the provider text unchanged (no trimming side effect)", got)
	}
}

// TestScriptGenerationTranslatorFailsClosedWhenUnwired keeps the composition-gap
// behavior explicit: a nil port must fail, never return an empty success.
func TestScriptGenerationTranslatorFailsClosedWhenUnwired(t *testing.T) {
	var nilAdapter *scriptGenerationTranslator
	if _, err := nilAdapter.Translate(context.Background(), scriptgen.TranslationInput{}); err == nil {
		t.Fatal("nil adapter returned a nil error, want a composition-gap failure")
	}

	unwired := &scriptGenerationTranslator{}
	if _, err := unwired.Translate(context.Background(), scriptgen.TranslationInput{}); err == nil {
		t.Fatal("unwired adapter returned a nil error, want a composition-gap failure")
	}
}

// TestScriptGenerationTranslatorPropagatesProviderError keeps the typed error
// path intact: a provider failure must not be reworded into a generic error.
func TestScriptGenerationTranslatorPropagatesProviderError(t *testing.T) {
	sentinel := errTranslationProbe{}
	adapter := &scriptGenerationTranslator{port: &stubTranslationPort{err: sentinel}}

	if _, err := adapter.Translate(context.Background(), scriptgen.TranslationInput{}); err != sentinel {
		t.Fatalf("error = %v, want the provider sentinel unchanged", err)
	}
}

// errTranslationProbe is a distinguishable provider error.
type errTranslationProbe struct{}

func (errTranslationProbe) Error() string { return "provider exploded" }
