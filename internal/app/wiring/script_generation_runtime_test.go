package wiring

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
)

type runtimeTranslationPort struct{ name string }

func (*runtimeTranslationPort) Translate(context.Context, translation.TranslationCommand) (translation.TranslationResult, error) {
	return translation.TranslationResult{}, nil
}

func TestScriptGenerationUsesCanonicalTranslationProviderChain(t *testing.T) {
	canonical := &runtimeTranslationPort{name: "argos-with-ollama-fallback"}
	got := scriptGenerationTranslationPort(&ComposeRoot{
		TextTracks: &TextTrackBundle{Translator: canonical},
	})
	if got != canonical {
		t.Fatalf("script translation port = %v, want canonical text-track provider chain", got)
	}
}
