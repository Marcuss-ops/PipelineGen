package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// InternetImagesProcessor searches web images per canonical segment
// and attaches the resulting candidates to the VidRush payload.
type InternetImagesProcessor struct {
	searcher InternetImageSearcher
	metrics  VidRushMetrics
}

// NewInternetImagesProcessor creates an InternetImagesProcessor.
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
	if plan == nil || !plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() {
		return &PostProcessResult{}, nil
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
			"internet_images",
			updated.SegmentID,
			updated.TextHash,
			plan.Language,
			plan.Model,
			plan.PromptVersion,
			strings.Join(updated.Insights.ImageQueries, "\u0000"),
		)
		if !plan.MediaPlan.ForceRefreshAssets {
			if cached, ok := cacheLoad(&vidrushImageCache, cacheKey); ok {
				if cachedSeg, ok := cached.(scriptpkg.VidRushSegmentResult); ok {
					cachedSeg = cloneVidRushSegmentResult(cachedSeg)
					cachedSeg.Cache.InternetImages = "HIT_EXACT"
					updatedSegments = append(updatedSegments, cachedSeg)
					if p.metrics != nil {
						p.metrics.IncAssetCache("internet_images", true)
					}
					continue
				}
			}
		}

		candidates := make([]scriptpkg.SegmentAssetCandidate, 0, 5)
		if p.metrics != nil {
			p.metrics.IncAssetCache("internet_images", false)
			p.metrics.IncProviderRequest("internet_images")
		}
		seen := make(map[string]struct{}, 8)
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
				Limit:     5,
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
				key := strings.ToLower(strings.TrimSpace(cand.AssetID))
				if key == "" {
					key = strings.ToLower(strings.TrimSpace(cand.SourceURL))
				}
				if key == "" {
					key = strings.ToLower(strings.TrimSpace(cand.PreviewURL))
				}
				if key == "" {
					continue
				}
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				if cand.Provider == "" {
					cand.Provider = "internet_images"
				}
				if cand.RightsStatus == "" {
					cand.RightsStatus = "unknown"
				}
				if cand.SelectionReason == "" {
					cand.SelectionReason = "retrieved image candidate matching segment entity/query"
				}
				candidates = append(candidates, cand)
				if len(candidates) >= 5 {
					break
				}
			}
			if len(candidates) >= 5 {
				break
			}
		}

		if len(candidates) == 0 {
			updated.Cache.InternetImages = "MISS"
			updatedSegments = append(updatedSegments, updated)
			cacheStore(&vidrushImageCache, cacheKey, updated)
			continue
		}

		updated.Assets.Candidates = append(updated.Assets.Candidates, candidates...)
		updated.Assets.SecondaryImages = append(updated.Assets.SecondaryImages, candidates...)
		updated.Cache.InternetImages = "MISS"
		updatedSegments = append(updatedSegments, updated)
		cacheStore(&vidrushImageCache, cacheKey, updated)
	}

	return &PostProcessResult{
		VidRushSegments: updatedSegments,
		Warnings:        warnings,
		Changed:         len(updatedSegments) > 0,
	}, nil
}
