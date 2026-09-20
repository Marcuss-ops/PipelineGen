// Package translation — fallback_translator_test.go: unit tests for the
// primary/fallback TranslationPort chain (Argos → Ollama).
package translation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
)

type scriptedTranslator struct {
	res TranslationResult
	err error
}

func (s *scriptedTranslator) Translate(_ context.Context, cmd TranslationCommand) (TranslationResult, error) {
	if s.err != nil {
		return s.res, s.err
	}
	r := s.res
	if r.SourceLang == "" {
		r.SourceLang = cmd.SourceLang
	}
	if r.TargetLang == "" {
		r.TargetLang = cmd.TargetLang
	}
	return r, nil
}

func TestFallbackTranslator_PrimarySucceeds(t *testing.T) {
	primary := &scriptedTranslator{res: TranslationResult{TranslatedText: "argos-it", UsedProvider: "argos", UsedModel: "argos-en-it"}}
	fallback := &scriptedTranslator{res: TranslationResult{TranslatedText: "ollama-it", UsedProvider: "ollama"}}
	f := NewFallbackTranslator(primary, fallback, zap.NewNop())

	res, err := f.Translate(context.Background(), TranslationCommand{SourceLang: "en", TargetLang: "it", Text: "hello"})
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if res.UsedProvider != "argos" {
		t.Fatalf("expected provider 'argos', got %q", res.UsedProvider)
	}
	if res.TranslatedText != "argos-it" {
		t.Fatalf("expected primary output, got %q", res.TranslatedText)
	}
}

func TestFallbackTranslator_PrimaryError_FallsBack(t *testing.T) {
	primary := &scriptedTranslator{err: errors.New("package missing")}
	fallback := &scriptedTranslator{res: TranslationResult{TranslatedText: "ollama-it", UsedProvider: "ollama", UsedModel: "gemma3:4b"}}
	f := NewFallbackTranslator(primary, fallback, zap.NewNop())

	res, err := f.Translate(context.Background(), TranslationCommand{SourceLang: "en", TargetLang: "it", Text: "hello"})
	if err != nil {
		t.Fatalf("expected nil error from fallback, got: %v", err)
	}
	if res.UsedProvider != "ollama" {
		t.Fatalf("expected provider 'ollama', got %q", res.UsedProvider)
	}
}

func TestFallbackTranslator_PrimaryEmpty_FallsBack(t *testing.T) {
	primary := &scriptedTranslator{res: TranslationResult{TranslatedText: "", UsedProvider: "argos"}}
	fallback := &scriptedTranslator{res: TranslationResult{TranslatedText: "ollama-it", UsedProvider: "ollama"}}
	f := NewFallbackTranslator(primary, fallback, zap.NewNop())

	res, err := f.Translate(context.Background(), TranslationCommand{SourceLang: "en", TargetLang: "it", Text: "hello"})
	if err != nil {
		t.Fatalf("expected nil error from fallback, got: %v", err)
	}
	if res.UsedProvider != "ollama" {
		t.Fatalf("expected fallback provider 'ollama' after empty primary, got %q", res.UsedProvider)
	}
}

func TestFallbackTranslator_NilPrimary_UsesFallback(t *testing.T) {
	fallback := &scriptedTranslator{res: TranslationResult{TranslatedText: "ollama-it", UsedProvider: "ollama"}}
	f := NewFallbackTranslator(nil, fallback, zap.NewNop())

	res, err := f.Translate(context.Background(), TranslationCommand{SourceLang: "en", TargetLang: "it", Text: "hello"})
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if res.UsedProvider != "ollama" {
		t.Fatalf("expected provider 'ollama', got %q", res.UsedProvider)
	}
}

func TestFallbackTranslator_NoProvider(t *testing.T) {
	f := NewFallbackTranslator(nil, nil, zap.NewNop())
	_, err := f.Translate(context.Background(), TranslationCommand{SourceLang: "en", TargetLang: "it", Text: "hello"})
	if err == nil || !strings.Contains(err.Error(), "no provider available") {
		t.Fatalf("expected 'no provider available' error, got: %v", err)
	}
}

func TestFallbackTranslator_BothFail(t *testing.T) {
	primary := &scriptedTranslator{err: errors.New("argos down")}
	fallback := &scriptedTranslator{err: errors.New("ollama down")}
	f := NewFallbackTranslator(primary, fallback, zap.NewNop())

	_, err := f.Translate(context.Background(), TranslationCommand{SourceLang: "en", TargetLang: "it", Text: "hello"})
	if err == nil || !strings.Contains(err.Error(), "ollama down") {
		t.Fatalf("expected fallback error to propagate, got: %v", err)
	}
}

// TestFallbackTranslator_DegeneratePrimaryFallsBack pins the quality gate: a
// non-empty but degenerate primary answer (here: the source copied verbatim)
// must NOT be returned; the request continues to the fallback provider.
func TestFallbackTranslator_DegeneratePrimaryFallsBack(t *testing.T) {
	primary := &scriptedTranslator{res: TranslationResult{TranslatedText: "hello world", UsedProvider: "argos"}}
	fallback := &scriptedTranslator{res: TranslationResult{TranslatedText: "ciao mondo", UsedProvider: "ollama"}}
	f := NewFallbackTranslator(primary, fallback, zap.NewNop())

	res, err := f.Translate(context.Background(), TranslationCommand{SourceLang: "en", TargetLang: "it", Text: "hello world"})
	if err != nil {
		t.Fatalf("expected the fallback answer, got error: %v", err)
	}
	if res.UsedProvider != "ollama" || res.TranslatedText != "ciao mondo" {
		t.Fatalf("expected the fallback provider answer, got provider=%q text=%q", res.UsedProvider, res.TranslatedText)
	}
}

// TestFallbackTranslator_DegenerateFallbackFailsLoud pins the fail-closed edge:
// when the fallback is ALSO degenerate there is no acceptable answer, so the
// chain returns a typed error instead of a wrong subtitle.
func TestFallbackTranslator_DegenerateFallbackFailsLoud(t *testing.T) {
	primary := &scriptedTranslator{err: errors.New("argos down")}
	fallback := &scriptedTranslator{res: TranslationResult{TranslatedText: "hello world", UsedProvider: "ollama"}}
	f := NewFallbackTranslator(primary, fallback, zap.NewNop())

	_, err := f.Translate(context.Background(), TranslationCommand{SourceLang: "en", TargetLang: "it", Text: "hello world"})
	var degenerate *ErrDegenerateTranslation
	if !errors.As(err, &degenerate) {
		t.Fatalf("expected *ErrDegenerateTranslation, got: %v", err)
	}
	if degenerate.TargetLang != "it" {
		t.Fatalf("typed error target = %q, want it", degenerate.TargetLang)
	}
}

// TestFallbackTranslator_DegeneratePrimaryWithoutFallbackFailsLoud pins the
// no-fallback edge: the answer is still surfaced (so the caller can inspect it)
// alongside the typed error.
func TestFallbackTranslator_DegeneratePrimaryWithoutFallbackFailsLoud(t *testing.T) {
	primary := &scriptedTranslator{res: TranslationResult{TranslatedText: "hello world", UsedProvider: "argos"}}
	f := NewFallbackTranslator(primary, nil, zap.NewNop())

	res, err := f.Translate(context.Background(), TranslationCommand{SourceLang: "en", TargetLang: "it", Text: "hello world"})
	var degenerate *ErrDegenerateTranslation
	if !errors.As(err, &degenerate) {
		t.Fatalf("expected *ErrDegenerateTranslation, got: %v", err)
	}
	if res.TranslatedText != "hello world" {
		t.Fatalf("expected the rejected answer to be surfaced, got %q", res.TranslatedText)
	}
}
