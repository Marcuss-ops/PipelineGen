// Package adapters — processor_clip_search.go (PR-CLIP-SEARCH-WIRING, July 2026).
//
// ClipSearchProcessor searches for Artlist clips per canonical
// segment using the per-segment queries produced by the entities
// processor.
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
	"fmt"
	"strings"

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
	metrics  VidRushMetrics
}

// NewClipSearchProcessor creates a ClipSearchProcessor. searcher
// may be nil at construction time — Process() returns empty results
// (no error) when the searcher is nil (BestEffort semantics).
func NewClipSearchProcessor(searcher ArtlistClipSearcher, metrics ...VidRushMetrics) *ClipSearchProcessor {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &ClipSearchProcessor{searcher: searcher, metrics: m}
}

// Name returns the canonical processor name.
func (p *ClipSearchProcessor) Name() ProcessorName { return ProcessorClipSearch }

// Policy classifies clip_search as BestEffort. The plan arg is
// accepted for interface uniformity; the policy is static.
func (p *ClipSearchProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

// Process searches for Artlist clips matching the canonical
// per-segment Artlist queries.
//
// Short-circuits (returns empty PostProcessResult, no error) when:
//   - The searcher is nil (backend not wired)
//   - the artlist provider toggle is disabled
//   - no VidRush segments are available
//
// On searcher success, stores the matched clips in
// PostProcessResult.ArtlistClipSuggestions and the per-segment
// VidRush payload. On searcher error (unavailable adapter), returns
// a warning.
func (p *ClipSearchProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil || !plan.MediaPlan.ProviderPolicy.Artlist.AsBool() {
		return &PostProcessResult{}, nil
	}
	if p.searcher == nil {
		return &PostProcessResult{
			Changed:  true,
			Warnings: []string{"clip_search: ArtlistClipSearcher not configured"},
		}, nil
	}
	if len(input.VidRushSegments) == 0 {
		return &PostProcessResult{}, nil
	}

	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input.VidRushSegments))
	aggregated := make([]ArtlistClipMatch, 0)
	var warnings []string
	for _, seg := range input.VidRushSegments {
		updated := cloneVidRushSegmentResult(seg)
		if len(updated.Insights.ArtlistQueries) == 0 {
			updated.Cache.Artlist = "BYPASSED"
			segments = append(segments, updated)
			continue
		}

		cacheKey := segmentCacheKey(
			"artlist",
			updated.SegmentID,
			updated.TextHash,
			plan.Language,
			plan.Model,
			plan.PromptVersion,
			strings.Join(updated.Insights.ArtlistQueries, "\u0000"),
		)
		if !plan.MediaPlan.ForceRefreshAssets {
			if cached, ok := cacheLoad(&vidrushArtlistCache, cacheKey); ok {
				if cachedSeg, ok := cached.(scriptpkg.VidRushSegmentResult); ok {
					cachedSeg = cloneVidRushSegmentResult(cachedSeg)
					cachedSeg.Cache.Artlist = "HIT_EXACT"
					segments = append(segments, cachedSeg)
					aggregated = append(aggregated, matchesFromSegment(cachedSeg)...)
					if p.metrics != nil {
						p.metrics.IncAssetCache("artlist", true)
					}
					continue
				}
			}
		}

		var segmentMatches []ArtlistClipMatch
		if p.metrics != nil {
			p.metrics.IncAssetCache("artlist", false)
			p.metrics.IncProviderRequest("artlist")
		}
		for _, query := range updated.Insights.ArtlistQueries {
			matches := p.searcher.SearchClips(ctx, plan.Title, []string{query})
			if len(matches) == 0 {
				continue
			}
			segmentMatches = append(segmentMatches, matches...)
		}
		if len(segmentMatches) == 0 {
			updated.Cache.Artlist = "MISS"
			segments = append(segments, updated)
			warnings = append(warnings, fmt.Sprintf("clip_search: no matching Artlist clips found for segment %s", updated.SegmentID))
			cacheStore(&vidrushArtlistCache, cacheKey, updated)
			continue
		}

		candidates := artlistMatchesToCandidates(updated, segmentMatches)
		updated.Assets.Candidates = append(updated.Assets.Candidates, candidates...)
		if len(candidates) > 0 {
			primary := candidates[0]
			updated.Assets.PrimaryVideo = &primary
		}
		updated.Cache.Artlist = "MISS"
		segments = append(segments, updated)
		aggregated = append(aggregated, segmentMatches...)
		cacheStore(&vidrushArtlistCache, cacheKey, updated)
	}

	if len(segments) == 0 {
		return &PostProcessResult{}, nil
	}
	return &PostProcessResult{
		VidRushSegments:        segments,
		ArtlistClipSuggestions: aggregated,
		Warnings:               warnings,
		Changed:                true,
	}, nil
}

func matchesFromSegment(seg scriptpkg.VidRushSegmentResult) []ArtlistClipMatch {
	if len(seg.Assets.Candidates) == 0 {
		return nil
	}
	out := make([]ArtlistClipMatch, 0, len(seg.Assets.Candidates))
	for _, cand := range seg.Assets.Candidates {
		if strings.TrimSpace(cand.Provider) != "artlist" {
			continue
		}
		out = append(out, ArtlistClipMatch{
			Phrase:         cand.Query,
			ClipNames:      []string{strings.TrimSpace(cand.Entity)},
			ClipDriveLinks: []string{cand.DriveLink},
		})
	}
	return out
}

func artlistMatchesToCandidates(seg scriptpkg.VidRushSegmentResult, matches []ArtlistClipMatch) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(matches))
	for i, match := range matches {
		score := 1.0 - float64(i)*0.05
		if score < 0.1 {
			score = 0.1
		}
		assetID := segmentCacheKey(seg.SegmentID, match.Phrase, strings.Join(match.ClipNames, "\u0000"), strings.Join(match.ClipDriveLinks, "\u0000"))
		candidate := scriptpkg.SegmentAssetCandidate{
			AssetID:         "artlist-" + assetID[:12],
			Provider:        "artlist",
			Query:           strings.TrimSpace(match.Phrase),
			Score:           score,
			RightsStatus:    "unknown",
			SelectionReason: "highest ranked Artlist candidate for segment query",
		}
		if len(match.ClipDriveLinks) > 0 {
			candidate.SourceURL = strings.TrimSpace(match.ClipDriveLinks[0])
			candidate.PreviewURL = candidate.SourceURL
			candidate.DriveLink = candidate.SourceURL
		}
		if len(match.ClipNames) > 0 {
			candidate.Entity = strings.TrimSpace(match.ClipNames[0])
		}
		out = append(out, candidate)
	}
	return out
}
