// Package generated — prompt_composer_test.go is the canonical TDD
// coverage for the PromptComposer (FASE 3, July 2026, image-territories
// action plan). Locks the contract surface so future refactors do not
// regress the idempotency guarantee or the regex-safe TrimSpace rule.
package generated

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
)

// fakeStyle builds a ResolvedStyle via struct literal (the generation
// package does not expose a constructor; the StyleResolver is canonical).
func fakeStyle(id string, suffix string) generation.ResolvedStyle {
	return generation.ResolvedStyle{ID: id, PromptSuffix: suffix}
}

func fakeComposer() PromptComposer {
	return NewPromptComposer()
}

// TestPromptComposer_BasicComposition locks the user-spec example:
// promptOriginal="castle" + style.PromptSuffix="cinematic lighting"
// → promptFinal="castle, cinematic lighting".
func TestPromptComposer_BasicComposition(t *testing.T) {
	got, err := fakeComposer().Compose(
		context.Background(),
		GenerateCommand{Prompt: "castle"},
		fakeStyle("cinematic", "cinematic lighting"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PromptFinal != "castle, cinematic lighting" {
		t.Fatalf("PromptFinal = %q, want %q", got.PromptFinal, "castle, cinematic lighting")
	}
	if got.PromptOriginal != "castle" {
		t.Fatalf("PromptOriginal = %q, want %q (must NOT be mutated)", got.PromptOriginal, "castle")
	}
}

// TestPromptComposer_Idempotent locks the "NON doppia applicazione"
// guarantee: calling Compose twice with identical input MUST NOT
// double-append the suffix.
func TestPromptComposer_Idempotent(t *testing.T) {
	c := fakeComposer()
	style := fakeStyle("cinematic", "cinematic lighting")

	first, err := c.Compose(context.Background(), GenerateCommand{Prompt: "castle"}, style)
	if err != nil {
		t.Fatalf("first Compose err: %v", err)
	}

	second, err := c.Compose(context.Background(), GenerateCommand{
		Prompt: first.PromptFinal,
	}, style)
	if err != nil {
		t.Fatalf("second Compose err: %v", err)
	}

	if first.PromptFinal != second.PromptFinal {
		t.Fatalf("non-idempotent: first=%q second=%q (must be equal)",
			first.PromptFinal, second.PromptFinal)
	}
}

// TestPromptComposer_IdempotentCaseInsensitive: case differences do NOT
// cause the suffix to be re-applied.
func TestPromptComposer_IdempotentCaseInsensitive(t *testing.T) {
	c := fakeComposer()
	style := fakeStyle("cinematic", "Cinematic Lighting")

	first, err := c.Compose(context.Background(), GenerateCommand{
		Prompt: "castle, CINEMATIC LIGHTING ",
	}, style)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if first.PromptFinal != "castle, CINEMATIC LIGHTING" {
		t.Fatalf("case-insensitive idempotency failed: got %q", first.PromptFinal)
	}
}

// TestPromptComposer_EmptySuffixPassThrough: no suffix means no decoration.
func TestPromptComposer_EmptySuffixPassThrough(t *testing.T) {
	got, err := fakeComposer().Compose(
		context.Background(),
		GenerateCommand{Prompt: "castle"},
		fakeStyle("cinematic", ""),
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.PromptFinal != "castle" {
		t.Fatalf("PromptFinal = %q, want %q", got.PromptFinal, "castle")
	}
}

// TestPromptComposer_EmptyPromptFallback: empty prompt + non-empty suffix
// means the suffix becomes the base (no leading ", ").
func TestPromptComposer_EmptyPromptFallback(t *testing.T) {
	got, err := fakeComposer().Compose(
		context.Background(),
		GenerateCommand{Prompt: ""},
		fakeStyle("cinematic", "cinematic lighting"),
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.PromptFinal != "cinematic lighting" {
		t.Fatalf("PromptFinal = %q, want %q", got.PromptFinal, "cinematic lighting")
	}
}

// TestPromptComposer_NegativePromptPopulated: NegativePrompt is copied
// verbatim from the resolved style.
func TestPromptComposer_NegativePromptPopulated(t *testing.T) {
	style := generation.ResolvedStyle{ID: "cinematic", PromptSuffix: "cinematic lighting", NegativePrompt: "blurry, deformed hands"}
	got, err := fakeComposer().Compose(
		context.Background(),
		GenerateCommand{Prompt: "castle"},
		style,
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.NegativePrompt != "blurry, deformed hands" {
		t.Fatalf("NegativePrompt = %q, want %q", got.NegativePrompt, "blurry, deformed hands")
	}
}

// TestPromptComposer_RegexSafeTrimSpace: tab + newline + CR must be stripped.
func TestPromptComposer_RegexSafeTrimSpace(t *testing.T) {
	got, err := fakeComposer().Compose(
		context.Background(),
		GenerateCommand{Prompt: "\t\n castle \r\n"},
		fakeStyle("cinematic", "  cinematic lighting  "),
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.PromptFinal != "castle, cinematic lighting" {
		t.Fatalf("PromptFinal = %q, want %q (regex-safe trim failed)",
			got.PromptFinal, "castle, cinematic lighting")
	}
}

// TestPromptComposer_DimensionFallback: caller-supplied dims win, else style.
func TestPromptComposer_DimensionFallback(t *testing.T) {
	style := generation.ResolvedStyle{ID: "cinematic", PromptSuffix: "cinematic lighting", Width: 1280, Height: 720}

	explicit, err := fakeComposer().Compose(
		context.Background(),
		GenerateCommand{Prompt: "castle", Width: 1920, Height: 1080},
		style,
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if explicit.Width != 1920 || explicit.Height != 1080 {
		t.Fatalf("explicit dims should win: got %dx%d", explicit.Width, explicit.Height)
	}

	fallback, err := fakeComposer().Compose(
		context.Background(),
		GenerateCommand{Prompt: "castle"},
		style,
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if fallback.Width != 1280 || fallback.Height != 720 {
		t.Fatalf("style-dim fallback failed: got %dx%d, want 1280x720",
			fallback.Width, fallback.Height)
	}
}

// TestPromptComposer_ContextCancelled: a cancelled context is honoured.
func TestPromptComposer_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fakeComposer().Compose(
		ctx,
		GenerateCommand{Prompt: "castle"},
		fakeStyle("cinematic", "cinematic lighting"),
	)
	if err == nil {
		t.Fatalf("expected error on cancelled context, got nil")
	}
}

// TestPromptComposer_StyleProvenance: StyleID + StyleVersion populate.
func TestPromptComposer_StyleProvenance(t *testing.T) {
	style := generation.ResolvedStyle{ID: "cinematic-v2", PromptSuffix: "cinematic lighting", Width: 1920, Height: 1080, Version: 2}

	got, err := fakeComposer().Compose(
		context.Background(),
		GenerateCommand{Prompt: "castle"},
		style,
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.StyleID != "cinematic-v2" {
		t.Fatalf("StyleID = %q, want %q", got.StyleID, "cinematic-v2")
	}
	if got.StyleVersion != 2 {
		t.Fatalf("StyleVersion = %d, want 2", got.StyleVersion)
	}
}

// checkSubstringCount returns the number of non-overlapping occurrences
// of `sub` in `s`. Used to lock the non-double-application guarantee.
func checkSubstringCount(s, sub string) int {
	if sub == "" {
		return 0
	}
	count := 0
	for i := 0; i+len(sub) <= len(s); {
		if s[i:i+len(sub)] == sub {
			count++
			i += len(sub)
			continue
		}
		i++
	}
	return count
}
