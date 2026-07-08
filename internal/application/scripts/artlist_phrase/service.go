// Package artlist_phrase — service.go: PhraseAssetSearchService
// implementation (PR-POSTPROCESSOR-UNIFICATION-PHASE-4, July 2026,
// deadline 2026-08-22).
//
// godlike/06 SSOT (one canonical owner per fact): the service
// struct + ctor + SearchPhrases live ONLY here. The two ports
// (PhraseTranslator + PhraseAssetSearcher) are declared in
// ports.go. The preprocessing helpers (DedupeEmpty, TranslateEach,
// contextualQuery, mergeHits) live in preprocess.go.
package artlist_phrase

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
)

// SearchMaxHits is the canonical cap on per-phrase search results.
// Matches the legacy SearchScriptAssets(ctx, svc, queries, targets, 2)
// limit argument (internal/api/script/flow.go::SearchScriptAssets).
const SearchMaxHits = 2

// PhraseAssetSearchService folds the artlist-phrase preprocessing
// (translation + search) into a single canonical service. Pre-PR
// the preprocessing was split between:
//
//  1. processor_clip_search.go::Process — dedupe + filter empty
//     phrases inline, then call searcher.SearchClips(ctx, title, phrases).
//  2. flow_helpers_artlist.go::SearchArtlistClips — for each phrase:
//     artlistSearchPhrase(ctx, svc, phrase) (translation) + then
//     SearchScriptAssets(ctx, svc, queries, targets, 2) (realtime
//     search).
//
// The new service owns the full pipeline:
//
//	DedupeEmpty → TranslateEach (per-phrase typed failure) →
//	SearchAssets(per-phrase, translated + contextual) → mergeHits →
//	[]PhraseMatch.
//
// The pre-PR ArtlistClipSearcher port and usecase.SearchArtlistClips
// function stay as thin wrappers (7-day soak pattern per
// PR-POSTPROCESSOR-UNIFICATION-PHASE-3) that delegate to this
// service and map []PhraseMatch to their legacy return types.
type PhraseAssetSearchService struct {
	translator PhraseTranslator
	searcher   PhraseAssetSearcher
}

// NewService creates a PhraseAssetSearchService. Both translator and
// searcher are required at construction. nil translator surfaces a
// typed ErrTranslatorNil per-phrase at SearchPhrases time (godlike/07
// NO-FAKE-AVAILABILITY — callers see a typed diagnostic rather than
// a nil-pointer panic). nil searcher is tolerated: the per-phrase
// search returns empty (the translation step still runs so the
// caller sees the translated phrase + empty Clips).
//
// godlike/07 fail-closed at composition: the service is the
// canonical owner of "phrase preprocessing for artlist search";
// every composition site that wires artlist clip search MUST
// construct it via this ctor.
func NewService(translator PhraseTranslator, searcher PhraseAssetSearcher) *PhraseAssetSearchService {
	return &PhraseAssetSearchService{
		translator: translator,
		searcher:   searcher,
	}
}

// SearchPhrases translates each input phrase and searches for
// matching assets. Returns one PhraseMatch per unique, non-empty
// input phrase (preserving first-occurrence order). An empty or
// nil phrases slice returns nil with nil error.
//
// Per-phrase behavior:
//   - Translation failure (translator error or empty translation):
//     PhraseMatch.TranslationError is populated with the error
//     message; TranslatedPhrase is ""; Clips is nil. godlike/07
//     NO-FAKE-AVAILABILITY — the search is NOT attempted on the
//     untranslated phrase.
//   - Translation success + nil searcher: PhraseMatch.TranslatedPhrase
//     is populated; Clips is nil (no search backend wired).
//   - Translation success + searcher error: PhraseMatch.TranslatedPhrase
//     is populated; Clips is nil (search error swallowed; the
//     BestEffort policy of the ClipSearchProcessor treats empty
//     clips as a soft-fail, not a hard error).
//   - Translation success + searcher success: PhraseMatch.TranslatedPhrase
//     is populated; Clips has up to SearchMaxHits merged results
//     (translated-query hits first, contextual-query hits appended).
//
// godlike/07 typed-error contract: the function itself never returns
// an error. All failure modes are encoded per-phrase in the
// PhraseMatch.TranslationError field. This matches the pre-PR
// behavior (SearchArtlistClips returned []ScriptArtlistClipSuggestion
// with per-suggestion TranslationError, never a function-level
// error).
func (s *PhraseAssetSearchService) SearchPhrases(ctx context.Context, title string, phrases []string) []PhraseMatch {
	deduped := DedupeEmpty(phrases)
	if len(deduped) == 0 {
		return nil
	}

	// 1. Translate each phrase (failures captured per-phrase).
	translations := TranslateEach(ctx, s.translator, deduped)

	// 2 + 3. For each successfully translated phrase, search +
	// compose PhraseMatch. Preserves deduped order.
	out := make([]PhraseMatch, 0, len(deduped))
	for _, phrase := range deduped {
		match := PhraseMatch{Phrase: phrase}
		tr, ok := translations[phrase]
		if !ok {
			// Defensive: TranslateEvery populates every deduped phrase.
			// If a key is missing, treat as an empty-translation error
			// so the caller sees a typed diagnostic.
			match.TranslationError = ErrEmptyTranslation.Error()
			out = append(out, match)
			continue
		}
		if tr.Err != nil {
			match.TranslationError = tr.Err.Error()
			out = append(out, match)
			continue
		}
		match.TranslatedPhrase = tr.Translated

		if s.searcher != nil {
			hits := s.searchPhrasePair(ctx, title, tr.Translated)
			match.Clips = hits
		}
		out = append(out, match)
	}
	return out
}

// searchPhrasePair issues the two-query search (translated +
// contextual) and returns the merged result capped at SearchMaxHits.
// Per godlike/07 BestEffort semantics: any error from the searcher
// is swallowed (the ClipSearchProcessor treats empty clips as a
// soft-fail, not a hard error). Per godlike/07 NO-FAKE-AVAILABILITY:
// the per-phrase failure is recorded in the PhraseMatch's
// TranslationError field by the caller; this helper returns the
// raw hit slice.
func (s *PhraseAssetSearchService) searchPhrasePair(ctx context.Context, title, translated string) []ports.AssetSearchHit {
	query := ports.AssetSearchQuery{
		Source:    "artlist",
		MediaType: "video",
		Limit:     SearchMaxHits,
	}

	query.Query = translated
	h1, _ := s.searcher.SearchAssets(ctx, query)

	contextual := contextualQuery(title, translated)
	if contextual == "" || contextual == translated {
		// Title was empty (or identical to translated) — no point
		// in a duplicate search. Return h1 directly (already
		// capped at SearchMaxHits by the adapter).
		return h1
	}

	query.Query = contextual
	h2, _ := s.searcher.SearchAssets(ctx, query)

	return mergeHits(h1, h2, SearchMaxHits)
}
