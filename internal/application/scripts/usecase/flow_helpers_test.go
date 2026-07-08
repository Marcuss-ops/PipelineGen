// Package usecase — tests for the SearchArtlistClips caller-level
// P0.6 no-silent-fallback translator contract. The per-phrase
// translation-failure contract is now owned by the canonical
// artlist_phrase.PhraseAssetSearchService (see
// internal/application/scripts/artlist_phrase/service_test.go for
// the 16 hermetic TDD tests covering DedupeEmpty, TranslateEach,
// contextualQuery, mergeHits, and the full SearchPhrases pipeline).
//
// This file retains the caller-level regression test for
// SearchArtlistClips to lock the godlike/07 no-fake-availability
// guarantee: a translation error cannot cause the caller to
// silently use the original phrase as the Qdrant search query.
package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// Pattern 0 (AGENTS.md, June 2026): the stub below satisfies the
// production `TranslatorService` interface (field declared at
// services.go:27) implicitly via its `TranslateTextWithModel`
// method. We cannot route through
// `internal/application/translation.TranslatorFunc` because that
// 3-arg function type is incompatible with the 4-arg production
// signature `(ctx, text, lang, model)` — Go disallows conversion
// between function types with different parameter counts.

// stubFailingTranslator returns an error for every Translate call.
type stubFailingTranslator struct{}

func (s *stubFailingTranslator) TranslateTextWithModel(_ context.Context, _, _, _ string) (string, error) {
	return "", errors.New("forced translation failure (P0.6 test)")
}

// TestSearchArtlistClips_TranslationFailurePropagatesToCaller is the
// caller-level P0.6 gate. It verifies that the SilentSuccess path
// (searching Qdrant with the original phrase) is gone: when the
// translator fails, the returned ScriptArtlistClipSuggestion surfaces
// the error and intentionally has NO Clips (no fake-success search).
func TestSearchArtlistClips_TranslationFailurePropagatesToCaller(t *testing.T) {
	svc := ClipServices{
		Translator:    &stubFailingTranslator{},
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
	}
}

// Compile-time assertion: the stub satisfies the production
// `TranslatorService` interface declared in services.go via its
// `TranslateTextWithModel` method. *ollama.Generator already does
// this implicitly at production wiring; the test mirrors the
// interface boilerplate here so a future signature drift raises a
// build error instead of a runtime panic.
var _ TranslatorService = (*stubFailingTranslator)(nil)
