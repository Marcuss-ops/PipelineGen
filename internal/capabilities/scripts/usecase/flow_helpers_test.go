// Package usecase — tests for the SearchArtlistClips caller-level
// P0.6 no-silent-fallback translator contract. The per-phrase
// translation-failure contract is now owned by the canonical
// artlist_phrase.PhraseAssetSearchService (see
// internal/capabilities/scripts/artlist_phrase/service_test.go for
// the 16 hermetic TDD tests covering DedupeEmpty, TranslateEach,
// contextualQuery, mergeHits, and the full SearchPhrases pipeline).
//
// This file retains the caller-level regression test for
// SearchArtlistClips to lock the godlike/07 no-fake-availability
// guarantee: a translation error cannot cause the caller to
// silently use the original phrase as the Qdrant search query.
//
// PR-DEADC-SCRIPTS-CLIP-SERVICES-PER-USE-CASE-DEP-BAGS Step 1+2 (July 2026):
// the test now wires the canonical `TranslationPort` surface (not
// the retired `Translator` field). The stub satisfies
// translation.TranslationPort.Translate; nil-port is a separate
// regression below.
package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	translation "github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
)

// stubFailingTranslationPort returns an error for every Translate call.
type stubFailingTranslationPort struct{}

func (s *stubFailingTranslationPort) Translate(_ context.Context, _ translation.TranslationCommand) (translation.TranslationResult, error) {
	return translation.TranslationResult{}, errors.New("forced translation failure (P0.6 test)")
}

// stubEmptyTranslationPort returns a zero-value TranslationResult
// (TranslatedText="") — the production adapter surfaces this as
// "artlist translation returned empty text" error, which must
// propagate (no silent Qdrant search over the original phrase).
type stubEmptyTranslationPort struct{}

func (s *stubEmptyTranslationPort) Translate(_ context.Context, _ translation.TranslationCommand) (translation.TranslationResult, error) {
	return translation.TranslationResult{TranslatedText: ""}, nil
}

// TestSearchArtlistClips_TranslationFailurePropagatesToCaller is the
// caller-level P0.6 gate. It verifies that the SilentSuccess path
// (searching Qdrant with the original phrase) is gone: when the
// translator fails, the returned ScriptArtlistClipSuggestion surfaces
// the error and intentionally has NO Clips (no fake-success search).
func TestSearchArtlistClips_TranslationFailurePropagatesToCaller(t *testing.T) {
	svc := ClipServices{
		TranslationPort: &stubFailingTranslationPort{},
		Logger:          zap.NewNop(),
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

// TestSearchArtlistClips_EmptyTranslationPropagatesToCaller pins the
// "returned empty text" branch: even when the provider returns
// success with empty TranslatedText, the adapter fails closed
// (godlike/07 NO-FAKE-AVAILABILITY — never silent empty success).
func TestSearchArtlistClips_EmptyTranslationPropagatesToCaller(t *testing.T) {
	svc := ClipServices{
		TranslationPort: &stubEmptyTranslationPort{},
		Logger:          zap.NewNop(),
	}
	results := SearchArtlistClips(context.Background(), svc, "title", []string{"italian phrase"})
	if assert.Len(t, results, 1) {
		sug := results[0]
		assert.NotEmpty(t, sug.TranslationError,
			"TranslationError MUST be populated when provider returns empty text (no fake-success)")
		assert.Empty(t, sug.Clips,
			"Clips MUST be empty when translation returns empty text")
	}
}

// TestSearchArtlistClips_NilTranslationPort_FailsClosed pins the
// nil-port fail-closed contract: a missing TranslationPort returns
// a typed error (never a silent fallback to the original phrase).
// godlike/07 NO-FAKE-AVAILABILITY.
func TestSearchArtlistClips_NilTranslationPort_FailsClosed(t *testing.T) {
	svc := ClipServices{
		TranslationPort: nil,
		Logger:          zap.NewNop(),
	}
	results := SearchArtlistClips(context.Background(), svc, "title", []string{"italian phrase"})
	if assert.Len(t, results, 1) {
		sug := results[0]
		assert.NotEmpty(t, sug.TranslationError,
			"TranslationError MUST be populated when TranslationPort is nil")
		assert.Empty(t, sug.Clips)
	}
}

// Compile-time assertions: each stub satisfies the canonical
// translation.TranslationPort. *ollama.Generator already satisfies
// this implicitly at production wiring; the test mirrors the
// compile-time pin so a future TranslationPort signature drift
// raises a build error instead of a runtime panic.
var (
	_ translation.TranslationPort = (*stubFailingTranslationPort)(nil)
	_ translation.TranslationPort = (*stubEmptyTranslationPort)(nil)
)

// TestSearchArtlistClips_NilTranslationPort_SentinelPropagates locks
// the godlike/07 typed-error contract on the nil-port path: the
// adapter MUST return ErrArtlistTranslationUnavailable (not a bare
// errors.New) so callers can probe via errors.Is. Per
// PR-DEADC-SCRIPTS-CLIP-SERVICES-PER-USE-CASE-DEP-BAGS Step 1+2
// code-reviewer MUST-FIX #1 (typed-error contract).
func TestSearchArtlistClips_NilTranslationPort_SentinelPropagates(t *testing.T) {
	svc := ClipServices{
		TranslationPort: nil,
		Logger:          zap.NewNop(),
	}
	results := SearchArtlistClips(context.Background(), svc, "title", []string{"italian phrase"})
	if assert.Len(t, results, 1) {
		sug := results[0]
		assert.NotEmpty(t, sug.TranslationError)
		assert.Contains(t, sug.TranslationError, ErrArtlistTranslationUnavailable.Error(),
			"nil-port MUST surface the typed ErrArtlistTranslationUnavailable sentinel (godlike/07 typed-error contract)")
		assert.Empty(t, sug.Clips)
	}
}

// TestSearchArtlistClips_EmptyTranslation_SentinelPropagates locks
// the godlike/07 typed-error contract on the empty-translation path:
// the adapter MUST return ErrArtlistTranslationEmpty (not a bare
// errors.New) so callers can probe via errors.Is. Per
// PR-DEADC-SCRIPTS-CLIP-SERVICES-PER-USE-CASE-DEP-BAGS Step 1+2
// code-reviewer MUST-FIX #1.
func TestSearchArtlistClips_EmptyTranslation_SentinelPropagates(t *testing.T) {
	svc := ClipServices{
		TranslationPort: &stubEmptyTranslationPort{},
		Logger:          zap.NewNop(),
	}
	results := SearchArtlistClips(context.Background(), svc, "title", []string{"italian phrase"})
	if assert.Len(t, results, 1) {
		sug := results[0]
		assert.NotEmpty(t, sug.TranslationError)
		assert.Contains(t, sug.TranslationError, ErrArtlistTranslationEmpty.Error(),
			"empty-translation MUST surface the typed ErrArtlistTranslationEmpty sentinel (godlike/07 typed-error contract)")
		assert.Empty(t, sug.Clips)
	}
}
