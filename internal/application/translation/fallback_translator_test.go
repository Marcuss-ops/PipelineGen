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
