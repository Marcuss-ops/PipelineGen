package script

import (
	"context"
	"fmt"
	"strings"

	"velox/go-master/internal/repository/clips"
	artlistSvc "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/association"
	"velox/go-master/internal/service/visualquery"
	segmentnorm "velox/go-master/internal/service/catalognormalizer"
	"velox/go-master/pkg/sliceutil"
)

// buildSegment creates a TimelineSegment from raw LLM output
func buildSegment(ctx context.Context, req ScriptDocsRequest, rawSeg timelineLLMSegment, idx int, dataDir string, stockRepo *clips.Repository, assocService *association.Service, normalizer *segmentnorm.Service) TimelineSegment {
	seg := TimelineSegment{
		Index:             idx + 1,
		StartTime:        rawSeg.StartTime,
		EndTime:          rawSeg.EndTime,
		Timestamp:        fmt.Sprintf("%.0f-%.0f", rawSeg.StartTime, rawSeg.EndTime),
		Subject:          strings.TrimSpace(rawSeg.Subject),
		NarrativeText:    strings.TrimSpace(rawSeg.NarrativeText),
		Keywords:         rawSeg.Keywords,
		Entities:         rawSeg.Entities,
		SearchSuggestions: rawSeg.SearchSuggestions,
	}

	// Resolve subject
	if req.Topic != "" && strings.EqualFold(seg.Subject, req.Topic) {
		seg.Subject = resolveTimelineSegmentSubject(ctx, req, seg, dataDir, stockRepo, assocService)
	}

	// Normalize
	if normalized, _ := normalizer.NormalizeSegment(ctx, segmentnorm.SegmentInput{
		Topic:         req.Topic,
		Duration:      req.Duration,
		Template:      req.Template,
		Subject:       seg.Subject,
		NarrativeText: seg.NarrativeText,
		Keywords:      seg.Keywords,
		Entities:      seg.Entities,
	}); normalized != nil {
		seg.CanonicalSubject = normalized.CanonicalSubject
		seg.CanonicalKeywords = sliceutil.UniqueStrings(normalized.CanonicalKeywords)
		seg.CanonicalEntities = sliceutil.UniqueStrings(normalized.CanonicalEntities)
	}

	// Associate assets
	associateSegment(ctx, &seg, assocService, req.Topic)

	return seg
}

// populateVisualFields populates visual fields from batch results
func populateVisualFields(seg *TimelineSegment, batchResults map[int]visualquery.VisualQueryResult) {
	if batchResults == nil {
		return
	}
	if r, ok := batchResults[seg.Index]; ok {
		seg.VisualSubject = r.VisualSubject
		seg.VisualCaption = r.VisualCaption
		seg.SearchSuggestions = sliceutil.UniqueStrings(append(seg.SearchSuggestions, r.Queries...))
	}
}

// searchArtlistFromDB searches Artlist clips in the database only (no live search)
func searchArtlistFromDB(ctx context.Context, seg *TimelineSegment, artlistService *artlistSvc.Service) {
	if len(seg.SearchSuggestions) == 0 {
		return
	}
	
	var artlistClips []association.ScoredMatch
	for _, query := range seg.SearchSuggestions {
		clipPtrs := artlistService.SearchClips(ctx, query)
		for _, c := range clipPtrs {
			artlistClips = append(artlistClips, association.ScoredMatch{
				Title:  c.Name,
				Path:   c.LocalPath,
				Score:  80,
				Source: "artlist_dynamic",
				Link:   c.DriveLink,
			})
		}
	}
	if len(artlistClips) > 0 {
		seg.ArtlistMatches = artlistClips
	}
}
