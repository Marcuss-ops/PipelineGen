package script

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"velox/go-master/internal/repository/clips"
	artlistSvc "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/association"
	clipresolver "velox/go-master/internal/service/clipresolver"
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
		seg.VisualPrompts = r.VisualPrompts
		seg.EntityQueries = r.EntityQueries
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

// searchArtlistWithResolver searches Artlist clips using the ClipResolver for better recommendations
func searchArtlistWithResolver(ctx context.Context, seg *TimelineSegment, clipResolver *clipresolver.Service, topic string, usedClipIDs, usedFolderIDs, usedPaths []string) {
	if clipResolver == nil || len(seg.SearchSuggestions) == 0 {
		return
	}

	req := &clipresolver.RecommendRequest{
		Topic:         topic,
		SegmentID:     seg.Timestamp,
		SegmentText:   seg.NarrativeText,
		Queries:       seg.SearchSuggestions,
		EntityQueries: sliceutil.UniqueStrings(append(seg.CanonicalEntities, seg.EntityQueries...)),
		VisualPrompts: seg.VisualPrompts,
		UsedClipIDs:   usedClipIDs,
		UsedFolderIDs: usedFolderIDs,
		UsedPaths:     usedPaths,
		Limit:         5,
		MinScore:      0.5,
		Explain:       false,
		AutoHarvest:   true,
	}

	resp, err := clipResolver.Recommend(ctx, req)
	if err != nil {
		return
	}

	if len(resp.Recommended) > 0 {
		artlistClips := make([]association.ScoredMatch, 0, len(resp.Recommended))
		for _, rec := range resp.Recommended {
			match := association.ScoredMatch{
				ClipID: rec.ClipID,
				Title:  rec.Title,
				Path:   rec.LocalPath,
				Score:  int(rec.Score * 100),
				Source: "clip_resolver",
				Link:   rec.DriveLink,
			}

			// Add folder info if available
			if rec.FolderID != "" {
				match.FolderLink = "https://drive.google.com/drive/folders/" + rec.FolderID
				if rec.FolderPath != "" {
					match.FolderName = filepath.Base(rec.FolderPath)
				} else {
					match.FolderName = "Source Folder"
				}
			}
			artlistClips = append(artlistClips, match)
		}
		seg.ArtlistMatches = artlistClips
	} else if resp.NeedsHarvest {
		seg.SearchSuggestions = append(seg.SearchSuggestions, resp.HarvestTerms...)
	}
}
