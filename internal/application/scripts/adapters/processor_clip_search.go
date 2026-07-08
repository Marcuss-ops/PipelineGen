// Package adapters — processor_clip_search.go (PR-CLIP-SEARCH-WIRING, July 2026).
//
// ClipSearchProcessor searches for Artlist clips matching the
// artlist_phrases extracted by the upstream EntitiesProcessor.
//
// The processor reads input.Entities.ArtlistPhrases (populated by
// the EntitiesProcessor via mergePostProcessResult write-back) and
// dispatches them through the ArtlistClipSearcher port. Results are
// stored in PostProcessResult.ArtlistClipSuggestions.
//
// Policy is ProcessorBestEffort — clip search is an enrichment,
// NOT a hard gate. A missing or failing backend produces a warning
// but does not abort the pipeline.
//
// ORDERING DEPENDENCY: this processor MUST run AFTER the
// EntitiesProcessor in the plan's Postprocessors list. The
// EntitiesProcessor populates input.Entities.ArtlistPhrases via
// mergePostProcessResult write-back; without it, this processor
// sees nil Entities and short-circuits. The postprocessor pipeline
// runs in list-order, so ensure "entities" appears before
// "clip_search" in the plan.
//
// godlike/06 SSOT: ArtlistClipSearcher is the sole canonical port;
// declared in compat_adapters.go. The ClipSearchProcessor is the
// sole canonical consumer.
package adapters

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ProcessorClipSearch is declared in processor_names.go (canonical
// SOLE home per godlike/06 SSOT). This file consumes the constant via
// package-scope visibility; do NOT redeclare it here.

// ClipSearchProcessor queries the ArtlistClipSearcher port for
// matching clips, using the artlist_phrases extracted by the
// upstream EntitiesProcessor.
type ClipSearchProcessor struct {
	searcher ArtlistClipSearcher
}

// NewClipSearchProcessor creates a ClipSearchProcessor. searcher
// may be nil at construction time — Process() returns empty results
// (no error) when the searcher is nil (BestEffort semantics).
func NewClipSearchProcessor(searcher ArtlistClipSearcher) *ClipSearchProcessor {
	return &ClipSearchProcessor{searcher: searcher}
}

// Name returns the canonical processor name.
func (p *ClipSearchProcessor) Name() ProcessorName { return ProcessorClipSearch }

// Policy classifies clip_search as BestEffort. The plan arg is
// accepted for interface uniformity; the policy is static.
func (p *ClipSearchProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

// Process searches for Artlist clips matching the artlist_phrases
// from the entity extraction result.
//
// Short-circuits (returns empty PostProcessResult, no error) when:
//   - The searcher is nil (backend not wired)
//   - input.Entities is nil (entities processor didn't run or failed)
//   - input.Entities.ArtlistPhrases is empty (no phrases to search)
//
// On searcher success, stores the matched clips in
// PostProcessResult.ArtlistClipSuggestions. On searcher error
// (unavailable adapter), returns a warning.
func (p *ClipSearchProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.searcher == nil {
		return &PostProcessResult{
			Changed:  true,
			Warnings: []string{"clip_search: ArtlistClipSearcher not configured"},
		}, nil
	}
	if input.Entities == nil || len(input.Entities.ArtlistPhrases) == 0 {
		return &PostProcessResult{}, nil
	}

	// Dedup is owned by artlist_phrase.PhraseAssetSearchService
	// (PR-POSTPROCESSOR-UNIFICATION-PHASE-4, July 2026, deadline
	// 2026-08-22). The ArtlistClipSearcher port adapter
	// (wire_script_postprocess.go::artlistClipSearchAdapter)
	// delegates to the service which applies DedupeEmpty +
	// TranslateEach before the search. This processor no longer
	// duplicates the dedup logic inline.

	title := ""
	if plan != nil {
		title = plan.Title
	}
	matches := p.searcher.SearchClips(ctx, title, input.Entities.ArtlistPhrases)
	if len(matches) == 0 {
		return &PostProcessResult{
			Changed:  true,
			Warnings: []string{"clip_search: no matching Artlist clips found for the extracted phrases"},
		}, nil
	}

	return &PostProcessResult{
		ArtlistClipSuggestions: matches,
		Changed:                true,
	}, nil
}
