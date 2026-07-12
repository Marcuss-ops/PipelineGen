package generated

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

// TestGenerationRegistry_Generate_FailClosed pins the canonical
// Empty-registry fail-closed contract: with no GenerationProvider
// registered, the public Generate method must surface typed
// ErrProviderUnavailable (godlike/07 doctrine). The canonical image
// endpoint landed by `NewGenerationProviderRegistry(log, nil)` from
// the composition root relies on this.
func TestGenerationRegistry_Generate_FailClosed(t *testing.T) {
	registry := NewGenerationProviderRegistry(zap.NewNop(), nil)
	out, err := registry.Generate(context.Background(), GenerateRequest{Prompt: "x"}, GenerateOptions{})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
	if out != nil {
		t.Errorf("out = %v, want nil", out)
	}
}

// TestGenerationRegistry_Empty_NoProviders pins the Empty-registry
// accessor contract: Providers / ProviderByName / Diagnostics all
// surface zero providers / nil / empty maps when no provider is
// registered. These are separate test functions from the
// Resolve-fail-closed coverage in resolve_test.go to keep the
// per-capability surface legible.
func TestGenerationRegistry_Empty_NoProviders(t *testing.T) {
	registry := NewGenerationProviderRegistry(zap.NewNop(), nil)

	if got := registry.Providers(); len(got) != 0 {
		t.Fatalf("Providers() len = %d, want 0", len(got))
	}
	if got := registry.ProviderByName("google-slides"); got != nil {
		t.Fatalf("ProviderByName(\"google-slides\") = %v, want nil", got)
	}
	if got := registry.Diagnostics(context.Background()); len(got) != 0 {
		t.Fatalf("Diagnostics() len = %d, want 0", len(got))
	}
}
