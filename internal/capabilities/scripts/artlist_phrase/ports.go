// Package artlist_phrase — ports.go: canonical port surface for the
// PhraseAssetSearchService (PR-POSTPROCESSOR-UNIFICATION-PHASE-4,
// July 2026, deadline 2026-08-22).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - PhraseMatch lives ONLY here (the service's return type).
//   - PhraseTranslator + PhraseAssetSearcher live ONLY here (the
//     two ports the service consumes).
//   - AssetSearchPort (the Phase 3 unified port) is EMBEDDED by
//     PhraseAssetSearcher — single canonical search surface; no
//     port redefinition, no duplicate interface declaration.
package artlist_phrase

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

// PhraseMatch is the canonical return type for
// PhraseAssetSearchService.SearchPhrases. One PhraseMatch per unique,
// non-empty input phrase (preserving first-occurrence order).
//
// TranslationError (PR 0.6, June 2026) is the explicit error marker
// for the artlist-phrase → English translation step. Non-empty when
// the translator call (PhraseTranslator.Translate) failed or returned
// an empty string. When populated, Clips is intentionally empty (no
// silent fallback to the original phrase — godlike/07
// no-fake-availability). Phrase stays populated with the
// user-supplied input so the API response remains contract-stable.
//
// godlike/07 typed-error contract: TranslationError carries the
// error message string (informational only; the typed sentinel lives
// inside TranslateEach). Callers that need a typed probe should
// inspect the internal TranslateEach error via the service's test
// surface; runtime callers display the message verbatim.
type PhraseMatch struct {
	Phrase           string
	TranslatedPhrase string
	Clips            []ports.AssetSearchHit
	TranslationError string
}

// PhraseTranslator wraps the translation step. The concrete adapter
// (wired in internal/app/wire_script_postprocess.go as
// phraseTranslatorAdapter) composes the 3-tier fallback:
//  1. TranslationPort.Translate (Phase 9 canonical)
//  2. Translator.TranslateTextWithModel (legacy 4-arg)
//  3. Translation.TranslateText (legacy 3-arg)
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil translator or a translator
// error must surface as a non-empty TranslationError on the result;
// callers must not silently search on the untranslated input.
type PhraseTranslator interface {
	// Translate returns the translated phrase (English) or a non-nil
	// error. An empty translated string with nil error is treated as
	// an error condition by TranslateEach (the service captures it
	// as a typed empty-translation error so the caller sees the
	// failure path rather than a silent empty search).
	Translate(ctx context.Context, phrase string) (string, error)
}

// PhraseAssetSearcher embeds the canonical Phase 3 AssetSearchPort.
// The service uses SearchAssets per-phrase with Source="artlist" +
// MediaType="video" to match the legacy realtime-search contract
// (SearchScriptAssets in the canonical scripts/usecase package).
//
// godlike/06 SSOT: the embed preserves the Phase 3 migration
// discipline — the artlist_phrase package does NOT redefine the
// search interface, it consumes the canonical unified one.
type PhraseAssetSearcher interface {
	ports.AssetSearchPort
}
