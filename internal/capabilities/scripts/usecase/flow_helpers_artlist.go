// Package usecase — flow_helpers_artlist.go: artlist-phrase helpers
// (PR-POSTPROCESSOR-UNIFICATION-PHASE-4, July 2026, deadline
// 2026-08-22).
//
// Owns: phraseTranslatorAdapter + phraseSearcherAdapter (internal
// adapters that wrap ClipServices into the canonical artlist_phrase
// ports) + SearchArtlistClips (thin wrapper delegating to
// artlist_phrase.PhraseAssetSearchService.SearchPhrases).
//
// Pre-PR the translation + search logic was inline here
// (artlistSearchPhrase + SearchScriptAssets per-phrase loop). Now
// the preprocessing is owned by the canonical artlist_phrase
// package (godlike/06 SSOT one-canonical-owner-per-fact). This
// file stays as the 7-day-soak thin wrapper per the Phase 3
// migration pattern (usecase.SearchArtlistClips signature
// preserved byte-stable; the refactor is internal to the
// implementation).
//
// CUTOVER (forward-pointer PR-PHASE-4-CUTOVER): the wire file
// will pre-build *artlist_phrase.PhraseAssetSearchService at
// composition time and pass it through ClipServices (or a new
// dedicated field); the per-call service construction in
// SearchArtlistClips will be replaced with a direct delegate.
package usecase

import (
	"context"
	"errors"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/artlist_phrase"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
)

// Typed sentinels (godlike/07 typed-error contract). Each sentinel
// has a domain-prefixed name (ArtlistTranslation*) so callers can
// probe via errors.Is(err, ErrArtlistTranslationX) for "is this a
// translation config bug or a translator failure?".
//
// Per PR-DEADC-SCRIPTS-CLIP-SERVICES-PER-USE-CASE-DEP-BAGS Step 1+2
// code-reviewer MUST-FIX #1: bare errors.New failed the typed-error
// contract (callers could not probe the error class). The 2 sentinels
// below are reachable via errors.Is and document the 2 failure modes
// of the canonical TranslationPort path.
var (
	// ErrArtlistTranslationUnavailable signals a missing
	// TranslationPort on ClipServices — composition root misconfig.
	ErrArtlistTranslationUnavailable = errors.New("artlist translation unavailable: TranslationPort not wired")

	// ErrArtlistTranslationEmpty signals a translator success with
	// empty TranslatedText — provider is misconfigured or returned
	// a no-op. godlike/07 NO-FAKE-AVAILABILITY: never silent empty
	// success.
	ErrArtlistTranslationEmpty = errors.New("artlist translation returned empty text")
)

// SearchArtlistClips translates each candidate phrase and returns
// per-phrase artlist suggestions. Thin wrapper around the canonical
// artlist_phrase.PhraseAssetSearchService (PR-POSTPROCESSOR-
// UNIFICATION-PHASE-4, July 2026).
//
// A translation failure is explicit: the phrase is preserved,
// Clips stays empty, and TranslationError is populated so callers
// never silently search on the untranslated input (godlike/07
// NO-FAKE-AVAILABILITY contract preserved from pre-PR).
//
// godlike/06 SSOT: the preprocessing is owned by
// artlist_phrase.PhraseAssetSearchService. This function is a
// thin wrapper that maps the service's []PhraseMatch return type
// to the legacy []ScriptArtlistClipSuggestion wire shape. After
// the 7-day soak, the CUTOVER phase will retire this wrapper in
// favor of a direct service call from the caller
// (insight_builder.go).
func SearchArtlistClips(ctx context.Context, svc ClipServices, title string, phrases []string) []ScriptArtlistClipSuggestion {
	deduped := artlist_phrase.DedupeEmpty(phrases)
	if len(deduped) == 0 {
		return nil
	}

	// Construct the canonical service inline with per-call adapters.
	// The service is lightweight (two port fields) so per-call
	// construction is acceptable during the 7-day soak. The wire
	// file (artlistClipSearchAdapter.SearchClips) takes the SAME
	// path: it delegates to this function, so the service is
	// constructed once per processor invocation. CUTOVER will move
	// the construction to composition time.
	service := artlist_phrase.NewService(
		&phraseTranslatorAdapter{svc: svc},
		&phraseSearcherAdapter{svc: svc},
	)
	matches := service.SearchPhrases(ctx, title, deduped)

	// Map []PhraseMatch → []ScriptArtlistClipSuggestion (legacy
	// wire shape, preserved byte-stable for the 7-day soak).
	out := make([]ScriptArtlistClipSuggestion, 0, len(matches))
	for _, m := range matches {
		sug := ScriptArtlistClipSuggestion{Phrase: m.Phrase}
		if m.TranslationError != "" {
			sug.TranslationError = m.TranslationError
		}
		if len(m.Clips) > 0 {
			sug.Clips = convertPhraseMatchClips(m.Clips)
			// Best-effort fold the top result into the legacy
			// artlist-facing folder fields so callers get a usable
			// link when the search returns artlist hits. Preserved
			// from the pre-PR SearchArtlistClips behavior.
			top := sug.Clips[0]
			sug.FolderLink = strings.TrimSpace(top.DriveLink)
			sug.FolderName = strings.TrimSpace(top.Name)
			sug.FolderID = strings.TrimSpace(top.ID)
		}
		out = append(out, sug)
	}
	return out
}

// convertPhraseMatchClips maps []ports.AssetSearchHit (canonical
// Phase 3 SSOT) → []ScriptAssetSuggestion (legacy usecase wire
// shape). The field names differ (AssetID vs ID); the rest are
// byte-stable.
func convertPhraseMatchClips(hits []ports.AssetSearchHit) []ScriptAssetSuggestion {
	out := make([]ScriptAssetSuggestion, 0, len(hits))
	for _, h := range hits {
		out = append(out, ScriptAssetSuggestion{
			ID:        h.AssetID,
			Name:      h.Name,
			Source:    h.Source,
			Score:     h.Score,
			DriveLink: h.DriveLink,
		})
	}
	return out
}

// ── Internal adapters (wrap ClipServices → artlist_phrase ports) ─────────

// phraseTranslatorAdapter wraps ClipServices into the canonical
// artlist_phrase.PhraseTranslator port. Single path: TranslationPort
// (Phase 1+2 closure, July 2026, retired the legacy
// Translation/Translator 2-tier fallback). Pre-PR the equivalent
// logic was in artlistSearchPhrase (flow_helpers_artlist.go,
// retired in PHASE-4 Commit 1).
//
// godlike/06 SSOT: the adapter is internal to the usecase package;
// the canonical PhraseTranslator port lives at
// internal/capabilities/scripts/artlist_phrase/ports.go.
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil TranslationPort returns a
// typed error (never silently empty text + auto-fallback to the
// original phrase). Callers surface the error per PhraseMatch
// wire shape (see artlist_phrase.PhraseMatch.TranslationError).
type phraseTranslatorAdapter struct {
	svc ClipServices
}

func (a *phraseTranslatorAdapter) Translate(ctx context.Context, phrase string) (string, error) {
	phrase = strings.TrimSpace(phrase)
	if phrase == "" {
		return "", errors.New("artlist search phrase is empty")
	}

	if a.svc.TranslationPort == nil {
		return "", ErrArtlistTranslationUnavailable
	}
	res, err := a.svc.TranslationPort.Translate(ctx, translation.TranslationCommand{
		SourceLang: "auto",
		TargetLang: "en",
		Text:       phrase,
	})
	if err != nil {
		if a.svc.Logger != nil {
			a.svc.Logger.Warn("artlist translation failed via TranslationPort", zap.String("phrase", phrase), zap.Error(err))
		}
		return "", err
	}
	translated := strings.TrimSpace(res.TranslatedText)
	if translated == "" {
		return "", ErrArtlistTranslationEmpty
	}
	return translated, nil
}

// DefaultArtlistMinScore is the canonical fallback minimum score
// for artlist clip search. Matches the legacy SearchScriptAssets
// (internal/capabilities/scripts/usecase/clip_source.go) hard-coded
// value of 0.7. Named here (per code-review SHOULD-FIX) so future
// operators can grep for the literal without diving into the
// adapter body.
const DefaultArtlistMinScore = 0.7

// phraseSearcherAdapter wraps ClipServices into the canonical
// artlist_phrase.PhraseAssetSearcher port. Calls
// RealtimeSvc.SearchClips per-query and converts
// []RealtimeMatchAsset → []ports.AssetSearchHit. The default
// minScore of DefaultArtlistMinScore matches the legacy
// SearchScriptAssets hard-coded value; the default limit of
// artlist_phrase.SearchMaxHits (2) matches the legacy per-call
// limit argument.
//
// godlike/06 SSOT: the adapter is internal to the usecase package;
// the canonical PhraseAssetSearcher port lives at
// internal/capabilities/scripts/artlist_phrase/ports.go.
type phraseSearcherAdapter struct {
	svc ClipServices
}

func (a *phraseSearcherAdapter) SearchAssets(ctx context.Context, q ports.AssetSearchQuery) ([]ports.AssetSearchHit, error) {
	if a.svc.RealtimeSvc == nil {
		return nil, nil
	}
	source := q.Source
	if source == "" {
		source = "artlist"
	}
	mediaType := q.MediaType
	if mediaType == "" {
		mediaType = "video"
	}
	limit := q.Limit
	if limit <= 0 {
		limit = artlist_phrase.SearchMaxHits
	}
	minScore := q.MinScore
	if minScore <= 0 {
		minScore = DefaultArtlistMinScore
	}

	matches, err := a.svc.RealtimeSvc.SearchClips(ctx, q.Query, source, mediaType, limit, minScore)
	if err != nil {
		return nil, err
	}

	hits := make([]ports.AssetSearchHit, 0, len(matches))
	for _, m := range matches {
		hits = append(hits, ports.AssetSearchHit{
			AssetID:   m.ID,
			Name:      m.Name,
			Source:    m.Source,
			Score:     m.Score,
			DriveLink: m.DriveLink,
		})
	}
	return hits, nil
}

// Compile-time pins (godlike/06 SSOT — port signature drift
// surfaces as build failure, not runtime panic).
var (
	_ artlist_phrase.PhraseTranslator    = (*phraseTranslatorAdapter)(nil)
	_ artlist_phrase.PhraseAssetSearcher = (*phraseSearcherAdapter)(nil)
)
