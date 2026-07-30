package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// internetImageCachePayload stores only the image-provider delta so a cache
// hit cannot replace Artlist candidates or other upstream segment state.
type internetImageCachePayload struct {
	Candidates []scriptpkg.SegmentAssetCandidate
}

// InternetImagesProcessor searches web images per canonical segment and
// attaches every unique result returned for the segment queries.
type InternetImagesProcessor struct {
	searcher InternetImageSearcher
	metrics  VidRushMetrics
}

func NewInternetImagesProcessor(searcher InternetImageSearcher, metrics ...VidRushMetrics) *InternetImagesProcessor {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &InternetImagesProcessor{searcher: searcher, metrics: m}
}

func (p *InternetImagesProcessor) Name() ProcessorName { return ProcessorInternetImages }

func (p *InternetImagesProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *InternetImagesProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil {
		return &PostProcessResult{}, nil
	}
	if !plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() {
		segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input.VidRushSegments))
		for _, segment := range input.VidRushSegments {
			cloned := cloneVidRushSegmentResult(segment)
			cloned.Cache.InternetImages = "BYPASSED"
			segments = append(segments, cloned)
		}
		if len(segments) == 0 {
			return &PostProcessResult{}, nil
		}
		return &PostProcessResult{VidRushSegments: segments, Changed: true}, nil
	}
	if p.searcher == nil {
		return &PostProcessResult{
			Changed:  true,
			Warnings: []string{"internet_images: InternetImageSearcher not configured"},
		}, nil
	}
	if len(input.VidRushSegments) == 0 {
		return &PostProcessResult{}, nil
	}

	perQueryLimit := 10
	if plan.MediaPlan.Planner.CandidateLimit > 0 {
		perQueryLimit = plan.MediaPlan.Planner.CandidateLimit
	}
	if perQueryLimit > 50 {
		perQueryLimit = 50
	}

	updatedSegments := make([]scriptpkg.VidRushSegmentResult, 0, len(input.VidRushSegments))
	var warnings []string
	for _, seg := range input.VidRushSegments {
		updated := cloneVidRushSegmentResult(seg)
		if len(updated.Insights.ImageQueries) == 0 {
			updated.Cache.InternetImages = "BYPASSED"
			updatedSegments = append(updatedSegments, updated)
			continue
		}

		cacheKey := segmentCacheKey(
			"internet-images-assets-v2",
			updated.SegmentID,
			updated.TextHash,
			plan.Language,
			plan.Model,
			plan.PromptVersion,
			fmt.Sprintf("%d", perQueryLimit),
			strings.Join(updated.Insights.ImageQueries, "\u0000"),
		)
		if !plan.MediaPlan.ForceRefreshAssets {
			if cached, ok := cacheLoad(&vidrushImageCache, cacheKey); ok {
				if payload, ok := cached.(internetImageCachePayload); ok {
					candidates := append([]scriptpkg.SegmentAssetCandidate(nil), payload.Candidates...)
					updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, candidates)
					updated.Assets.SecondaryImages = appendProviderCandidatesUnique(updated.Assets.SecondaryImages, candidates)
					updated.Cache.InternetImages = "HIT_EXACT"
					updatedSegments = append(updatedSegments, updated)
					if p.metrics != nil {
						p.metrics.IncAssetCache("internet_images", true)
					}
					continue
				}
			}
		}

		if p.metrics != nil {
			p.metrics.IncAssetCache("internet_images", false)
			p.metrics.IncProviderRequest("internet_images")
		}

		candidates := make([]scriptpkg.SegmentAssetCandidate, 0, perQueryLimit*len(updated.Insights.ImageQueries))
		seen := make(map[string]struct{}, cap(candidates))
		firstEntity := ""
		if len(updated.Insights.Entities) > 0 {
			firstEntity = strings.TrimSpace(updated.Insights.Entities[0].Value)
		}
		for _, query := range updated.Insights.ImageQueries {
			results, err := p.searcher.SearchImages(ctx, InternetImageSearchRequest{
				SegmentID: updated.SegmentID,
				Query:     query,
				Entity:    firstEntity,
				TextHash:  updated.TextHash,
				Language:  plan.Language,
				Limit:     perQueryLimit,
				Provider:  "internet_images",
			})
			if err != nil {
				if p.metrics != nil {
					p.metrics.IncProviderFailure("internet_images")
				}
				warnings = append(warnings, fmt.Sprintf("internet_images: search failed for segment %s: %v", updated.SegmentID, err))
				continue
			}
			for _, cand := range results {
				if cand.Provider == "" {
					cand.Provider = "internet_images"
				}
				// Defense-in-depth: reject candidates from forbidden providers.
				// The binding gate (validVidRushCandidate) also rejects these,
				// but filtering at ingest time prevents forbidden candidates
				// from polluting cache entries.
				if strings.ToLower(strings.TrimSpace(cand.Provider)) != "internet_images" {
					continue
				}
				if cand.RightsStatus == "" {
					cand.RightsStatus = "unknown"
				}
				if cand.SelectionReason == "" {
					cand.SelectionReason = "retrieved image candidate matching a segment entity/query"
				}
				key := vidRushCandidateIdentity(cand)
				if key == "" {
					continue
				}
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				candidates = append(candidates, cand)
			}
		}

		updated.Cache.InternetImages = "MISS"
		if plan.MediaPlan.ForceRefreshAssets {
			updated.Cache.InternetImages = "REFRESHED"
		}
		if len(candidates) > 0 {
			updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, candidates)
			updated.Assets.SecondaryImages = appendProviderCandidatesUnique(updated.Assets.SecondaryImages, candidates)
			cacheStore(&vidrushImageCache, cacheKey, internetImageCachePayload{
				Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), candidates...),
			})
		}
		// Empty provider results are deliberately not cached because these
		// in-memory entries have no TTL and would otherwise become permanent.
		updatedSegments = append(updatedSegments, updated)
	}

	return &PostProcessResult{
		VidRushSegments: updatedSegments,
		Warnings:        warnings,
		Changed:         len(updatedSegments) > 0,
	}, nil
}
