package script

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/association"
	clipresolver "velox/go-master/internal/media/clipresolver"
	"velox/go-master/internal/media/visualquery"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/pkg/sliceutil"
	"velox/go-master/internal/repository/clips"
	artlistSvc "velox/go-master/internal/sources/artlist"
)

const timelineCacheVersion = "v20"

func BuildTimelinePlan(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, dataDir, nodeScraperDir, sourceText, narrative string, stockRepo, artlistRepo, clipsRepo *clips.Repository, artlistService *artlistSvc.Service, assocService *association.Service, clipResolver *clipresolver.Service) (*TimelinePlan, error) {
	startedAt := time.Now()
	zap.L().Info("Building timeline plan", zap.String("topic", req.Topic))

	cache := NewCache(clipsRepo, gen)
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

	normalizer := newCatalogNormalizerService(stockRepo, clipsRepo, artlistRepo, zap.L())

	var usedClipIDs []string
	var usedFolderIDs []string
	var usedPaths []string

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
				seg.VisualPrompts = visualResult.VisualPrompts
				seg.EntityQueries = visualResult.EntityQueries
			}
		}

		// Now search Artlist using ClipResolver (preferred) or fallback to DB search
		if clipResolver != nil && len(seg.SearchSuggestions) > 0 {
			searchArtlistWithResolver(ctx, &seg, clipResolver, req.Topic, usedClipIDs, usedFolderIDs, usedPaths)
		} else if artlistService != nil && len(seg.SearchSuggestions) > 0 {
			searchArtlistFromDB(ctx, &seg, artlistService)
		}

		// Update used lists
		for _, m := range seg.ArtlistMatches {
			if m.ClipID != "" {
				usedClipIDs = append(usedClipIDs, m.ClipID)
			}
			if m.Path != "" {
				usedPaths = append(usedPaths, strings.ToLower(m.Path))
				dir := filepath.Dir(m.Path)
				if dir != "." && dir != "/" {
					usedFolderIDs = append(usedFolderIDs, strings.ToLower(dir))
				}
			}
		}
		for _, m := range seg.StockMatches {
			if m.ClipID != "" {
				usedClipIDs = append(usedClipIDs, m.ClipID)
			}
			if m.Path != "" {
				usedPaths = append(usedPaths, strings.ToLower(m.Path))
				dir := filepath.Dir(m.Path)
				if dir != "." && dir != "/" {
					usedFolderIDs = append(usedFolderIDs, strings.ToLower(dir))
				}
			}
		}

		// If we still have no useful local matches, trigger a live Artlist discovery as a final fallback.
		if false && len(seg.StockMatches) == 0 && len(seg.ArtlistMatches) == 0 && artlistService != nil {
			if decision, ok := attemptLiveSearchDecision(ctx, req, seg, artlistService); ok {
				if len(decision.Matches) > 0 {
					seg.ArtlistMatches = decision.Matches
				}
			}
		}

		// SMART HARVESTING: If still no matches, try a live search for the most relevant suggestions
		if false && len(seg.ArtlistMatches) == 0 && artlistService != nil && len(seg.SearchSuggestions) > 0 {
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

				liveResp, runResp, err := artlistService.DiscoverAndQueueRun(ctx, suggestion, 3)
				if err == nil && liveResp != nil && len(liveResp.Clips) > 0 {
					zap.L().Info("Live discovery successful", zap.Int("clips_found", len(liveResp.Clips)), zap.String("term", suggestion))

					// Add the folder match if available
					if runResp != nil && runResp.TagFolderLink != "" {
						seg.ArtlistMatches = append(seg.ArtlistMatches, association.ScoredMatch{
							Title:  "Drive Folder: " + suggestion,
							Score:  100, // Top priority
							Source: "drive_folder_live",
							Link:   runResp.TagFolderLink,
							Reason: "target upload folder",
						})
					}

					for _, c := range liveResp.Clips {
						match := association.ScoredMatch{
							Title:  c.Name,
							Path:   c.LocalPath,
							Score:  95 - (i * 5), // Slightly lower score for secondary suggestions
							Source: "artlist_live_discovery",
							Reason: "live search: " + suggestion,
						}

						// Prefer true Drive links. If a clip has no Drive link yet, keep it out of the
						// rendered output so the pipeline failure is visible instead of hiding it behind a folder.
						if c.DriveLink != "" {
							match.Link = c.DriveLink
						}
						if match.FolderName == "" {
							match.FolderName = suggestion
						}

						seg.ArtlistMatches = append(seg.ArtlistMatches, match)
					}
					// If we found something, we can stop searching further suggestions for this segment
					break
				} else if err != nil {
					zap.L().Warn("Live discovery failed", zap.Error(err), zap.String("term", suggestion))
				}
			}
		}

		// Apply Hybrid Search (Semantic + Linear Scoring)
		if assocService != nil {
			queryEmb, err := assocService.GenerateEmbedding(ctx, seg.NarrativeText)
			if err == nil && len(queryEmb) > 0 {
				if len(seg.ArtlistMatches) > 0 {
					seg.ArtlistMatches = assocService.ScoreMedia(ctx, seg.NarrativeText, queryEmb, seg.ArtlistMatches)
				}
				if len(seg.StockMatches) > 0 {
					seg.StockMatches = assocService.ScoreMedia(ctx, seg.NarrativeText, queryEmb, seg.StockMatches)
				}
			} else {
				zap.L().Warn("failed to generate embedding for segment", zap.Error(err), zap.Int("segment_index", seg.Index))
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

	// Track usage feedback for all matched clips in the finalized timeline
	usedIDs := make([]string, 0, len(plan.Segments)*3)
	for _, seg := range plan.Segments {
		for _, m := range seg.StockMatches {
			if m.ClipID != "" {
				usedIDs = append(usedIDs, m.ClipID)
			}
		}
		for _, m := range seg.ArtlistMatches {
			if m.ClipID != "" {
				usedIDs = append(usedIDs, m.ClipID)
			}
		}
	}
	if len(usedIDs) > 0 && clipsRepo != nil {
		if err := clipsRepo.MarkClipsUsed(ctx, usedIDs); err != nil {
			zap.L().Warn("failed to update clip usage stats", zap.Error(err))
		} else {
			zap.L().Info("updated usage stats for clips", zap.Int("count", len(usedIDs)))
		}
	}

	zap.L().Info("timeline plan completed", zap.Duration("elapsed", time.Since(startedAt)))
	return plan, nil
}
