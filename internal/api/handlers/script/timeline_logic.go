package script

import (
	"context"
	"time"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/clips"
	artlistSvc "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/association"
	clipresolver "velox/go-master/internal/service/clipresolver"
	"velox/go-master/internal/service/visualquery"
	segmentnorm "velox/go-master/internal/service/catalognormalizer"
	"velox/go-master/internal/service/timeline"
	"velox/go-master/pkg/sliceutil"
	"go.uber.org/zap"
)

const timelineCacheVersion = "v14"

func BuildTimelinePlan(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, dataDir, nodeScraperDir, sourceText, narrative string, stockRepo, artlistRepo, clipsRepo *clips.Repository, artlistService *artlistSvc.Service, assocService *association.Service, clipResolver *clipresolver.Service) (*TimelinePlan, error) {
	startedAt := time.Now()
	zap.L().Info("Building timeline plan", zap.String("topic", req.Topic))

	cache := timeline.NewCache(clipsRepo, gen)
	cacheKey := cache.BuildKey(req.Topic, req.Template, sourceText, narrative, req.Duration)

	// Try cache first
	if rows, err := cache.LoadPlan(ctx, cacheKey); err == nil && len(rows) > 0 {
		zap.L().Info("timeline plan cache hit", zap.String("topic", req.Topic))
		return convertCacheRowsToPlan(rows), nil
	}

	// 1. LLM SEGMENTATION
	rawPlan, err := chooseTimelinePlanWithLLM(ctx, gen, req.Topic, req.Duration, sourceText, narrative)
	if err != nil {
		rawPlan = fallbackTimelinePlan(req.Topic, req.Duration, narrative)
	}

	// Apply structured timeline if available
	if structuredPlan, ok := buildStructuredTimelinePlan(req.Topic, req.Duration, sourceText); ok && len(structuredPlan.Segments) > 1 {
		rawPlan = structuredPlan
	}

	// Prepare segments for batch query generation
	var batchSegments []visualquery.BatchSegmentInput
	for i, rawSeg := range rawPlan.Segments {
		batchSegments = append(batchSegments, visualquery.BatchSegmentInput{
			Index:     i + 1,
			Subject:   rawSeg.Subject,
			Narrative: rawSeg.NarrativeText,
		})
	}

	// BATCH LLM QUERY GENERATION - always generate visual fields first
	var batchResults map[int]visualquery.VisualQueryResult
	if gen != nil && len(batchSegments) > 0 {
		batchResults = visualquery.GenerateBatchArtlistVisualQueries(ctx, gen, req.Topic, batchSegments, visualquery.DefaultMaxQueries)
	}

	// Build timeline plan
	plan := &TimelinePlan{
		PrimaryFocus:  req.Topic,
		SegmentCount:  len(rawPlan.Segments),
		TotalDuration: req.Duration,
		Segments:      make([]TimelineSegment, 0, len(rawPlan.Segments)),
	}

	normalizer := segmentnorm.NewService(stockRepo, clipsRepo, artlistRepo, zap.L())

	for i, rawSeg := range rawPlan.Segments {
		seg := buildSegment(ctx, req, rawSeg, i, dataDir, stockRepo, assocService, normalizer)

		// ALWAYS populate visual fields first (from batch or individual generation)
		populateVisualFields(&seg, batchResults)

		// If visual fields not populated, generate individually
		if seg.VisualSubject == "" && gen != nil {
			zap.L().Info("visual fields empty, generating individually",
				zap.Int("segment_index", seg.Index),
			)
			visualResult := visualquery.GenerateArtlistVisualQuery(ctx, gen, req.Topic, seg.Subject, seg.NarrativeText, visualquery.DefaultMaxQueries)
			if visualResult.VisualSubject != "" {
				seg.VisualSubject = visualResult.VisualSubject
				seg.VisualCaption = visualResult.VisualCaption
				seg.SearchSuggestions = sliceutil.UniqueStrings(append(seg.SearchSuggestions, visualResult.Queries...))
			}
		}

		// Now search Artlist using ClipResolver (preferred) or fallback to DB search
		if clipResolver != nil && len(seg.SearchSuggestions) > 0 {
			searchArtlistWithResolver(ctx, &seg, clipResolver, req.Topic, nil)
		} else if artlistService != nil && len(seg.SearchSuggestions) > 0 {
			searchArtlistFromDB(ctx, &seg, artlistService)
		}

		// SMART HARVESTING: If still no matches, try a live search for the most relevant suggestions
		if len(seg.ArtlistMatches) == 0 && artlistService != nil && len(seg.SearchSuggestions) > 0 {
			maxToSearch := 2
			if len(seg.SearchSuggestions) < maxToSearch {
				maxToSearch = len(seg.SearchSuggestions)
			}

			for i := 0; i < maxToSearch; i++ {
				suggestion := seg.SearchSuggestions[i]
				zap.L().Info("No local matches, triggering live Artlist discovery",
					zap.Int("segment_index", seg.Index),
					zap.String("suggestion", suggestion),
				)
				
				liveResp, _, err := artlistService.DiscoverAndQueueRun(ctx, suggestion, 3)
				if err == nil && liveResp != nil && len(liveResp.Clips) > 0 {
					zap.L().Info("Live discovery successful", zap.Int("clips_found", len(liveResp.Clips)), zap.String("term", suggestion))
					for _, c := range liveResp.Clips {
						link := c.DriveLink
						if link == "" {
							link = c.ExternalURL
						}
						if link == "" {
							link = c.DownloadLink
						}

						seg.ArtlistMatches = append(seg.ArtlistMatches, association.ScoredMatch{
							Title:  c.Name,
							Path:   c.LocalPath,
							Score:  95 - (i * 5), // Slightly lower score for secondary suggestions
							Source: "artlist_live_discovery",
							Link:   link,
							Reason: "live search: " + suggestion,
						})
					}
					// If we found something, we can stop searching further suggestions for this segment
					break
				} else if err != nil {
					zap.L().Warn("Live discovery failed", zap.Error(err), zap.String("term", suggestion))
				}
			}
		}

		// Finally, filter ALL matches for semantic relevance
		if !hasUsefulVisualMatch(seg, req.Topic) {
			zap.L().Warn("no useful visual match found for segment",
				zap.Int("segment_index", seg.Index),
				zap.String("subject", seg.Subject),
				zap.String("visual_subject", seg.VisualSubject),
				zap.Strings("search_suggestions", seg.SearchSuggestions),
			)
			// We no longer nullify matches here, allowing the renderer and score system to decide
		}

		// Store in cache and add to plan
		storeSegmentInCache(ctx, cache, cacheKey, req, seg, narrative)
		plan.Segments = append(plan.Segments, seg)
	}

	zap.L().Info("timeline plan completed", zap.Duration("elapsed", time.Since(startedAt)))
	return plan, nil
}
