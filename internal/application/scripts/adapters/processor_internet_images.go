package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// internetImageCachePayload stores only the image-provider delta so a cache
// hit cannot replace Artlist candidates or other upstream segment state.
type internetImageCachePayload struct {
	Candidates []scriptpkg.SegmentAssetCandidate
}

var (
	entityImageCache sync.Map // canonical entity key -> []SegmentAssetCandidate
	entityImageLocks sync.Map // canonical entity key -> *sync.Mutex
)

// InternetImagesProcessor searches web images per canonical segment and
// attaches every unique result returned for the segment queries.
type InternetImagesProcessor struct {
	searcher InternetImageSearcher
	metrics  VidRushMetrics
	cache    scriptports.VidRushCachePort
}

func NewInternetImagesProcessor(searcher InternetImageSearcher, metrics ...VidRushMetrics) *InternetImagesProcessor {
	return NewInternetImagesProcessorWithCache(searcher, nil, metrics...)
}

func NewInternetImagesProcessorWithCache(searcher InternetImageSearcher, cache scriptports.VidRushCachePort, metrics ...VidRushMetrics) *InternetImagesProcessor {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &InternetImagesProcessor{searcher: searcher, metrics: m, cache: cache}
}

func (p *InternetImagesProcessor) Name() ProcessorName { return ProcessorInternetImages }

func (p *InternetImagesProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *InternetImagesProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil {
		return &PostProcessResult{}, nil
	}
	entityImagesEnabled := plan.MediaPlan.Extraction.EntityImages.Enabled
	if !plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() && !entityImagesEnabled {
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
		imageQueries := updated.Insights.ImageQueries
		if entityImagesEnabled {
			if entityQueries := scenePrimaryEntityQueries(input.SpecScene, updated); len(entityQueries) > 0 {
				imageQueries = entityQueries
			}
		}
		if len(imageQueries) == 0 {
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
			strings.Join(imageQueries, "\u0000"),
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
			var persisted internetImageCachePayload
			if hit, cacheErr := loadVidRushPersistentJSON(ctx, p.cache, "internet_images", cacheKey, &persisted); cacheErr != nil {
				return nil, cacheErr
			} else if hit {
				persisted.Candidates = append([]scriptpkg.SegmentAssetCandidate(nil), persisted.Candidates...)
				updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, persisted.Candidates)
				updated.Assets.SecondaryImages = appendProviderCandidatesUnique(updated.Assets.SecondaryImages, persisted.Candidates)
				updated.Cache.InternetImages = "HIT_EXACT"
				cacheStore(&vidrushImageCache, cacheKey, persisted)
				updatedSegments = append(updatedSegments, updated)
				if p.metrics != nil {
					p.metrics.IncAssetCache("internet_images", true)
				}
				continue
			}
		}

		if p.metrics != nil {
			p.metrics.IncAssetCache("internet_images", false)
			p.metrics.IncProviderRequest("internet_images")
		}

		candidates := make([]scriptpkg.SegmentAssetCandidate, 0, perQueryLimit*len(imageQueries))
		seen := make(map[string]struct{}, cap(candidates))
		firstEntity := ""
		if len(updated.Insights.Entities) > 0 {
			firstEntity = strings.TrimSpace(updated.Insights.Entities[0].Value)
		}
		type queryResult struct {
			candidates []scriptpkg.SegmentAssetCandidate
			query      string
			err        error
		}
		queryResults, mapErr := concurrent.Map(ctx, imageQueries, 4, func(ctx context.Context, _ int, query string) (queryResult, error) {
			entityCacheKey := segmentCacheKey("entity-image-v1", strings.ToLower(strings.TrimSpace(query)), plan.Language)
			if entityImagesEnabled && !plan.MediaPlan.ForceRefreshAssets {
				if cached, ok := cacheLoad(&entityImageCache, entityCacheKey); ok {
					if cachedCandidates, ok := cached.([]scriptpkg.SegmentAssetCandidate); ok {
						return queryResult{candidates: append([]scriptpkg.SegmentAssetCandidate(nil), cachedCandidates...), query: query}, nil
					}
				}
				var persisted []scriptpkg.SegmentAssetCandidate
				if hit, err := loadVidRushPersistentJSON(ctx, p.cache, "entity_images", entityCacheKey, &persisted); err != nil {
					return queryResult{}, err
				} else if hit {
					cacheStore(&entityImageCache, entityCacheKey, persisted)
					return queryResult{candidates: persisted, query: query}, nil
				}
			}
			var entityLock *sync.Mutex
			if entityImagesEnabled {
				actual, _ := entityImageLocks.LoadOrStore(entityCacheKey, &sync.Mutex{})
				entityLock = actual.(*sync.Mutex)
				entityLock.Lock()
				defer entityLock.Unlock()
				if !plan.MediaPlan.ForceRefreshAssets {
					if cached, ok := cacheLoad(&entityImageCache, entityCacheKey); ok {
						if cachedCandidates, ok := cached.([]scriptpkg.SegmentAssetCandidate); ok {
							return queryResult{candidates: append([]scriptpkg.SegmentAssetCandidate(nil), cachedCandidates...), query: query}, nil
						}
					}
				}
			}
			providerStart := time.Now()
			results, err := p.searcher.SearchImages(ctx, InternetImageSearchRequest{
				SegmentID: updated.SegmentID,
				Query:     query,
				Entity:    firstEntity,
				TextHash:  updated.TextHash,
				Language:  plan.Language,
				Limit:     perQueryLimit,
				Provider:  "internet_images",
			})
			observeVidRushProviderDuration(p.metrics, "internet_images_search", time.Since(providerStart))
			if err == nil && entityImagesEnabled && len(results) > 0 {
				cacheStore(&entityImageCache, entityCacheKey, append([]scriptpkg.SegmentAssetCandidate(nil), results...))
				if cacheErr := storeVidRushPersistentJSON(ctx, p.cache, "entity_images", entityCacheKey, results); cacheErr != nil {
					return queryResult{}, cacheErr
				}
			}
			return queryResult{candidates: results, query: query, err: err}, nil
		})
		if mapErr != nil {
			warnings = append(warnings, fmt.Sprintf("internet_images: bounded query fan-out failed for segment %s: %v", updated.SegmentID, mapErr))
		}
		for _, queryResult := range queryResults {
			if queryResult.err != nil {
				if p.metrics != nil {
					p.metrics.IncProviderFailure("internet_images")
				}
				warnings = append(warnings, fmt.Sprintf("internet_images: search failed for segment %s: %v", updated.SegmentID, queryResult.err))
				continue
			}
			for _, cand := range queryResult.candidates {
				if cand.Provider == "" {
					cand.Provider = "internet_images"
				}
				if cand.Query == "" {
					cand.Query = queryResult.query
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
			if cacheErr := storeVidRushPersistentJSON(ctx, p.cache, "internet_images", cacheKey, internetImageCachePayload{
				Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), candidates...),
			}); cacheErr != nil {
				return nil, cacheErr
			}
		}
		// Empty provider results are deliberately not cached because these
		// in-memory entries have no TTL and would otherwise become permanent.
		updatedSegments = append(updatedSegments, updated)
	}

	updatedSpecScene := projectEntityImageBindings(input.SpecScene, updatedSegments, plan.MediaPlan.Extraction.EntityImages)
	return &PostProcessResult{
		VidRushSegments:  updatedSegments,
		UpdatedSpecScene: updatedSpecScene,
		SpecSceneChanged: len(updatedSpecScene.Scenes) > 0,
		Warnings:         warnings,
		Changed:          len(updatedSegments) > 0,
	}, nil
}

// projectEntityImageBindings attaches only provider candidates that are
// explicitly relevant to the primary entity. Generic scene candidates are
// never promoted to an entity image, preventing unrelated images from being
// presented as a person/org/place match.
func projectEntityImageBindings(spec scriptpkg.SpecSceneOutput, segments []scriptpkg.VidRushSegmentResult, policy mediadomain.EntityImagePolicy) scriptpkg.SpecSceneOutput {
	if !policy.Enabled || len(spec.Scenes) == 0 {
		return spec
	}
	out := cloneSpecSceneOutput(spec)
	allowed := map[string]bool{"PERSON": true, "ORG": true, "GPE": true}
	if len(policy.EntityTypes) > 0 {
		allowed = make(map[string]bool, len(policy.EntityTypes))
		for _, raw := range policy.EntityTypes {
			allowed[normalizeAnnotationType(raw)] = true
		}
	}
	for i := range out.Scenes {
		if out.Scenes[i].Annotations == nil {
			continue
		}
		seg := findSegmentForScene(out.Scenes[i], segments)
		for entityIndex := range out.Scenes[i].Annotations.PrimaryEntities {
			entity := &out.Scenes[i].Annotations.PrimaryEntities[entityIndex]
			if !allowed[normalizeAnnotationType(entity.Type)] {
				continue
			}
			entity.Image = &scriptpkg.EntityImageBinding{Status: "not_found"}
			if seg == nil {
				continue
			}
			if candidate, ok := findEntityImageCandidate(*entity, *seg); ok {
				entity.Image = &scriptpkg.EntityImageBinding{
					Status: "resolved", AssetID: candidate.AssetID,
					DriveLink: candidate.DriveLink, Source: candidate.Provider,
					License: candidate.RightsBasis,
				}
			}
		}
	}
	return out
}

func scenePrimaryEntityQueries(spec scriptpkg.SpecSceneOutput, segment scriptpkg.VidRushSegmentResult) []string {
	for _, scene := range spec.Scenes {
		if (scene.SegmentID != "" && scene.SegmentID != segment.SegmentID) ||
			(scene.SegmentID == "" && scene.ID != "" && scene.ID != segment.SceneID) {
			continue
		}
		if scene.Annotations == nil {
			return nil
		}
		queries := make([]string, 0, len(scene.Annotations.PrimaryEntities))
		seen := make(map[string]struct{}, len(queries))
		for _, entity := range scene.Annotations.PrimaryEntities {
			if entity.Type != "PERSON" && entity.Type != "ORG" && entity.Type != "GPE" {
				continue
			}
			query := strings.TrimSpace(entity.CanonicalName)
			if query == "" {
				query = strings.TrimSpace(entity.Text)
			}
			key := strings.ToLower(query)
			if query == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			queries = append(queries, query)
		}
		return queries
	}
	return nil
}

func findSegmentForScene(scene scriptpkg.SpecScene, segments []scriptpkg.VidRushSegmentResult) *scriptpkg.VidRushSegmentResult {
	for i := range segments {
		if scene.SegmentID != "" && scene.SegmentID == segments[i].SegmentID {
			return &segments[i]
		}
		if scene.ID != "" && scene.ID == segments[i].SceneID {
			return &segments[i]
		}
		if scene.SegmentID == "" && scene.ID == "" && scene.Index == segments[i].Position {
			return &segments[i]
		}
	}
	return nil
}

func findEntityImageCandidate(entity scriptpkg.AnnotatedEntity, seg scriptpkg.VidRushSegmentResult) (scriptpkg.SegmentAssetCandidate, bool) {
	want := strings.ToLower(strings.TrimSpace(entity.CanonicalName))
	if want == "" {
		want = strings.ToLower(strings.TrimSpace(entity.Text))
	}
	all := append(append([]scriptpkg.SegmentAssetCandidate(nil), seg.Assets.Candidates...), seg.Assets.SecondaryImages...)
	for _, candidate := range all {
		if !validVidRushCandidate(candidate) || strings.TrimSpace(candidate.AssetID) == "" {
			continue
		}
		entityText := strings.ToLower(strings.TrimSpace(candidate.Entity))
		query := strings.ToLower(strings.TrimSpace(candidate.Query))
		if entityText == want || strings.Contains(query, want) || strings.Contains(entityText, want) {
			return candidate, true
		}
	}
	return scriptpkg.SegmentAssetCandidate{}, false
}
