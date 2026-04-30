package script

import (
	"context"
	"fmt"
	"strings"
	"go.uber.org/zap"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/clips"
	artlistSvc "velox/go-master/internal/service/artlist"
)

// BuildTimelinePlan coordinates the LLM planning and asset matching.
func BuildTimelinePlan(ctx context.Context, gen *ollama.Generator, req ScriptDocsRequest, sourceText, narrative string, stockRepo, artlistRepo *clips.Repository, artlistService *artlistSvc.Service) (*TimelinePlan, error) {
	zap.L().Info("Building timeline plan", zap.String("topic", req.Topic))

	// 1. LLM SEGMENTATION
	rawPlan, err := chooseTimelinePlanWithLLM(ctx, gen, req.Duration, sourceText, narrative)
	if err != nil {
		zap.L().Warn("LLM timeline planning failed, using fallback", zap.Error(err))
		rawPlan = fallbackTimelinePlan(req.Topic, req.Duration, narrative)
	} else {
		zap.L().Info("LLM timeline planning successful", zap.Int("segments", len(rawPlan.Segments)))
	}

	plan := &TimelinePlan{
		PrimaryFocus:  req.Topic,
		SegmentCount:  len(rawPlan.Segments),
		TotalDuration: req.Duration,
		Segments:      make([]TimelineSegment, 0, len(rawPlan.Segments)),
	}

	// 2. ASSET MATCHING (STRICT PRIORITY)
	for i, rawSeg := range rawPlan.Segments {
		seg := TimelineSegment{
			Index:           i + 1,
			StartTime:       rawSeg.StartTime,
			EndTime:         rawSeg.EndTime,
			Timestamp:       fmt.Sprintf("%.0f-%.0f", rawSeg.StartTime, rawSeg.EndTime),
			Subject:         strings.TrimSpace(rawSeg.Subject),
			NarrativeText:   rawSeg.NarrativeText,
			OpeningSentence: rawSeg.OpeningSentence,
			Keywords:        rawSeg.Keywords,
		}

		// Cascading Search
		searchAssetsForSegment(ctx, req, &seg, stockRepo, artlistRepo, artlistService)
		
		plan.Segments = append(plan.Segments, seg)
	}

	return plan, nil
}

func searchAssetsForSegment(ctx context.Context, req ScriptDocsRequest, seg *TimelineSegment, stockRepo, artlistRepo *clips.Repository, artlistService *artlistSvc.Service) {
	query := seg.Subject
	if query == "" && len(seg.Keywords) > 0 {
		query = seg.Keywords[0]
	}
	if query == "" {
		query = req.Topic
	}
	
	terms := []string{query}
	if seg.Subject != "" {
		terms = append(terms, seg.Subject)
	}

	// Priority 1: Local Stock
	if stockRepo != nil {
		catalog, _ := loadClipsFromDB(ctx, stockRepo, "stock")
		matches := matchClipDriveCatalog(catalog, terms, 3)
		if len(matches) > 0 {
			seg.StockMatches = filterStrictMatches(matches, query)
			if len(seg.StockMatches) > 0 {
				return // Found in stock, STOP.
			}
		}
	}

	// Priority 2: Artlist Downloaded (DB)
	if artlistRepo != nil {
		catalog, _ := loadClipsFromDB(ctx, artlistRepo, "artlist")
		matches := matchClipDriveCatalog(catalog, terms, 3)
		if len(matches) > 0 {
			seg.ArtlistMatches = filterStrictMatches(matches, query)
			if len(seg.ArtlistMatches) > 0 {
				return // Found in Artlist DB, STOP.
			}
		}
	}

	// Priority 3: Artlist Live Search
	if artlistService != nil {
		liveResp, _, err := artlistService.DiscoverAndQueueRun(ctx, query, 3)
		if err == nil && liveResp != nil && len(liveResp.Clips) > 0 {
			seg.ArtlistMatches = modelClipsToScoredMatches(liveResp.Clips, "Live Search", "artlist_dynamic", "")
		}
	}
}

// filterStrictMatches ensures the query is actually part of the result name/title
func filterStrictMatches(matches []scoredMatch, query string) []scoredMatch {
	if query == "" { return matches }
	var filtered []scoredMatch
	q := strings.ToLower(query)
	for _, m := range matches {
		if strings.Contains(strings.ToLower(m.Title), q) || strings.Contains(strings.ToLower(m.Path), q) {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// fallbackTimelinePlan creates a basic segment if LLM fails
func fallbackTimelinePlan(topic string, duration int, narrative string) *timelineLLMPlan {
	return &timelineLLMPlan{
		PrimaryFocus: topic,
		Segments: []timelineLLMSegment{
			{
				Index:           1,
				StartTime:       0,
				EndTime:         float64(duration),
				Subject:         topic,
				NarrativeText:   narrative,
				OpeningSentence: "Inizio del video su " + topic,
			},
		},
	}
}
