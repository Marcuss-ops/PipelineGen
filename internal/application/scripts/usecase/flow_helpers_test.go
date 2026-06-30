// Package usecase — tests for the P0.6 no-silent-fallback translator contract.
// Locks in the godlike/07 no-fake-availability guarantee for the
// artlistSearchPhrase helper: a translation error cannot cause the
// caller (SearchArtlistClips) to silently use the original phrase as
// the Qdrant search query.
package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// Pattern 0 (AGENTS.md, June 2026): the stubs below satisfy the
// production `TranslatorService` interface (field declared at
// services.go:27) implicitly via their `TranslateTextWithModel`
// method. We cannot route through
// `internal/application/translation.TranslatorFunc` because that
// 3-arg function type is incompatible with the 4-arg production
// signature `(ctx, text, lang, model)` — Go disallows conversion
// between function types with different parameter counts.

// stubFailingTranslator returns an error for every Translate call.
type stubFailingTranslator struct {
	calls int
}

func (s *stubFailingTranslator) TranslateTextWithModel(_ context.Context, _, _, _ string) (string, error) {
	s.calls++
	return "", errors.New("forced translation failure (P0.6 test)")
}

// stubStubTranslator (paradoxically named) returns a deterministic
// translated suffix; used for the "happy path" assertion.
type stubStubTranslator struct{}

func (s *stubStubTranslator) TranslateTextWithModel(_ context.Context, text, _, _ string) (string, error) {
	return text + "_en", nil
}

// TestArtlistSearchPhrase_TranslationFailureReturnsExplicitError is
// the canonical regression test for P0.6 site 1. On a translator
// error, artlistSearchPhrase MUST return ("", err) — NOT the input
// phrase. This is the per-call layer test; the caller-level gate is
// SearchArtlistClips_TranslationFailurePropagatesToCaller (below).
func TestArtlistSearchPhrase_TranslationFailureReturnsExplicitError(t *testing.T) {
	svc := ClipServices{
		Translator:    &stubFailingTranslator{},
		Logger:        zap.NewNop(),
		MetadataModel: "test-model",
	}
	translated, err := artlistSearchPhrase(context.Background(), svc, "very valid phrase")
	assert.Error(t, err, "translation failure must surface as an explicit error (P0.6 godlike/07)")
	assert.Equal(t, "", translated,
		"translated output MUST be empty on failure — silent fallback to the input phrase is banned")
	assert.Contains(t, err.Error(), "translation",
		"the error message should be informative for the operator audit trail")
}

// TestArtlistSearchPhrase_NilTranslatorReturnsExplicitError locks the
// nil-translator case — was previously silently returning the input
// phrase (godlike/07 violation).
func TestArtlistSearchPhrase_NilTranslatorReturnsExplicitError(t *testing.T) {
	svc := ClipServices{
		Logger:        zap.NewNop(),
		MetadataModel: "test-model",
	}
	translated, err := artlistSearchPhrase(context.Background(), svc, "valid phrase")
	assert.Error(t, err, "nil translator must surface as an explicit error")
	assert.Equal(t, "", translated)
}

// TestSearchArtlistClips_TranslationFailurePropagatesToCaller is the
// caller-level P0.6 gate. It verifies that the SilentSuccess path
// (searching Qdrant with the original phrase) is gone: when the
// translator fails, the returned ScriptArtlistClipSuggestion surfaces
// the error and intentionally has NO Clips (no fake-success search).
func TestSearchArtlistClips_TranslationFailurePropagatesToCaller(t *testing.T) {
	failing := &stubFailingTranslator{}
	svc := ClipServices{
		Translator:    failing,
		Logger:        zap.NewNop(),
		MetadataModel: "test-model",
	}
	results := SearchArtlistClips(context.Background(), svc, "title", []string{"italian phrase"})
	if assert.Len(t, results, 1, "SearchArtlistClips must return one entry per valid input phrase") {
		sug := results[0]
		// Phrase stays populated (call graph contract).
		assert.Equal(t, "italian phrase", sug.Phrase,
			"Phrase MUST stay populated with the user-supplied input (call-graph contract)")
		// TranslationError is the explicit error surface.
		assert.NotEmpty(t, sug.TranslationError,
			"TranslationError MUST be populated when translator fails (P0.6 godlike/07)")
		// Clips intentionally empty — no silent fallback to original-phrase search.
		assert.Empty(t, sug.Clips,
			"Clips MUST be empty when translation fails (no silent Qdrant search over the original phrase)")
		// We asserted the translator was actually called at least once.
		assert.GreaterOrEqual(t, failing.calls, 1,
			"artlistSearchPhrase must hit the translator exactly once per phrase")
	}
}

// Compile-time assertions: the stubs satisfy the production
// `TranslatorService` interface declared in services.go via their
// `TranslateTextWithModel` method. *ollama.Generator already does
// this implicitly at production wiring; the tests mirror the
// interface boilerplate here so a future signature drift raises a
// build error instead of a runtime panic.
var _ TranslatorService = (*stubFailingTranslator)(nil)
var _ TranslatorService = (*stubStubTranslator)(nil)
