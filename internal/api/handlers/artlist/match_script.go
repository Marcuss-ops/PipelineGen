package artlistpipeline

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clipcache"
	"velox/go-master/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// MatchRequest is the unified request for segment-to-clip matching.
type MatchRequest struct {
	VideoID   string    `json:"video_id"`
	Segments  []Segment `json:"segments"`
	MaxClips  int       `json:"max_clips"` // per segment, default 3
}

// Segment represents a text segment with timing.
type Segment struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// MatchResult is the response for the match endpoint.
type MatchResult struct {
	VideoID       string              `json:"video_id"`
	TotalSegments int                 `json:"total_segments"`
	Segments      []SegmentMatchResult `json:"segments"`
	TotalClips    int                 `json:"total_clips"`
	Duration      time.Duration       `json:"duration_ms"`
}

// SegmentMatchResult holds the match results for a single segment.
type SegmentMatchResult struct {
	SegmentIdx  int                 `json:"segment_idx"`
	Text        string              `json:"text"`
	Start       float64             `json:"start"`
	End         float64             `json:"end"`
	SegmentHash string              `json:"segment_hash"`
	CacheHit    bool                `json:"cache_hit"`
	QueriesUsed []string            `json:"queries_used"`
	Clips       []MatchedClip       `json:"clips"`
}

// MatchedClip represents a matched clip for a segment.
type MatchedClip struct {
	ClipID     string `json:"clip_id"`
	Title      string `json:"title"`
	DriveURL   string `json:"drive_url,omitempty"`
	DrivePath  string `json:"drive_path,omitempty"`
	Score      float64 `json:"score"`
	AlreadyDL  bool   `json:"already_downloaded"`
}

// HandleMatchScript is the unified endpoint that does everything:
// - For each segment: expand queries → search Artlist → rank → dedup → return clips
// - Reuses cached clips from DB (by segment_hash)
// - Returns ready-to-download clip list per segment
func (h *Handler) HandleMatchScript(c *gin.Context) {
	startTime := time.Now()

	var req MatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request: " + err.Error()})
		return
	}

	if len(req.Segments) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "segments are required"})
		return
	}

	maxClips := req.MaxClips
	if maxClips <= 0 {
		maxClips = 3
	}

	// Process each segment
	result := &MatchResult{
		VideoID:       req.VideoID,
		TotalSegments: len(req.Segments),
		Segments:      make([]SegmentMatchResult, 0, len(req.Segments)),
	}

	for _, segment := range req.Segments {
		segmentResult, err := h.processSegment(c.Request.Context(), segment, req.VideoID, maxClips)
		if err != nil {
			logger.Warn("Failed to process segment",
				zap.Float64("segment_start", segment.Start),
				zap.Error(err))
			continue
		}

		result.Segments = append(result.Segments, *segmentResult)
		result.TotalClips += len(segmentResult.Clips)
	}

	result.Duration = time.Since(startTime)

	logger.Info("Match script completed",
		zap.String("video_id", req.VideoID),
		zap.Int("segments", len(result.Segments)),
		zap.Int("total_clips", result.TotalClips),
		zap.Duration("duration", result.Duration))

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"result": result,
	})
}

// processSegment handles a single segment: expand queries → search → rank → dedup.
func (h *Handler) processSegment(ctx context.Context, segment Segment, videoID string, maxClips int) (*SegmentMatchResult, error) {
	segmentHash := computeSegmentHash(segment.Text)

	// Check cache: have we already searched for this segment?
	cachedClips, cacheHit := h.checkSegmentCache(segmentHash, videoID)
	if cacheHit && len(cachedClips) > 0 {
		return &SegmentMatchResult{
			SegmentIdx:  int(segment.Start),
			Text:        segment.Text,
			Start:       segment.Start,
			End:         segment.End,
			SegmentHash: segmentHash,
			CacheHit:    true,
			Clips:       cachedClips,
		}, nil
	}

	// Expand queries semantically
	expanded, err := h.queryExpander.ExpandQueries(ctx, segment.Text, int(segment.Start))
	if err != nil {
		return nil, fmt.Errorf("query expansion failed: %w", err)
	}

	// Search Artlist in parallel with expanded queries
	searcher := NewParallelSearcher(h.artlistSrc, h.artlistDB, 20)
	clips, err := searcher.SearchWithExpandedQueries(ctx, expanded.Queries, maxClips*3)
	if err != nil {
		return nil, fmt.Errorf("parallel search failed: %w", err)
	}

	// Rank clips by relevance
	ranked := rankClipsByText(segment.Text, clips, maxClips)

	// Convert to MatchedClip format
	matchedClips := make([]MatchedClip, len(ranked))
	for i, clip := range ranked {
		matchedClips[i] = MatchedClip{
			ClipID:    clip.ID,
			Title:     clip.Title,
			DriveURL:  clip.DriveURL,
			DrivePath: clip.LocalPathDrive,
			Score:     float64(clip.Duration), // Simple scoring: longer = better
			AlreadyDL: clip.Downloaded,
		}
	}

	// Cache the result
	h.cacheSegmentResult(segmentHash, videoID, matchedClips)

	return &SegmentMatchResult{
		SegmentIdx:  int(segment.Start),
		Text:        segment.Text,
		Start:       segment.Start,
		End:         segment.End,
		SegmentHash: segmentHash,
		CacheHit:    false,
		QueriesUsed: expanded.Queries,
		Clips:       matchedClips,
	}, nil
}

// checkSegmentCache checks if we already have clips for this segment hash.
func (h *Handler) checkSegmentCache(segmentHash, videoID string) ([]MatchedClip, bool) {
	if h.clipCache == nil {
		return nil, false
	}

	clipRecords, found := h.clipCache.GetCachedSegments(segmentHash)
	if !found || len(clipRecords) == 0 {
		return nil, false
	}

	// Convert to MatchedClip format
	clips := make([]MatchedClip, len(clipRecords))
	for i, cr := range clipRecords {
		clips[i] = MatchedClip{
			ClipID:    cr.ClipID,
			Title:     cr.Title,
			DriveURL:  cr.DriveURL,
			DrivePath: cr.DrivePath,
			Score:     cr.Score,
			AlreadyDL: cr.Downloaded,
		}
	}

	return clips, true
}

// cacheSegmentResult stores the clip matches for future reuse.
func (h *Handler) cacheSegmentResult(segmentHash, videoID string, clips []MatchedClip) {
	if h.clipCache == nil {
		return
	}

	// Convert to ClipRecord format
	for _, mc := range clips {
		record := clipcache.ClipRecord{
			ClipID:     mc.ClipID,
			VideoID:    videoID,
			Title:      mc.Title,
			URL:        "",
			DriveURL:   mc.DriveURL,
			DrivePath:  mc.DrivePath,
			Score:      mc.Score,
			Downloaded: mc.AlreadyDL,
		}
		if err := h.clipCache.Store(&record); err != nil {
			logger.Warn("Failed to cache clip result",
				zap.String("clip_id", mc.ClipID),
				zap.Error(err))
		}
	}
}

// computeSegmentHash computes a SHA1 hash of the segment text for dedup.
// Delegates to clipcache for consistency across the codebase.
func computeSegmentHash(text string) string {
	return clipcache.ComputeSegmentHash(text)
}

// rankClipsByText ranks clips by text similarity and returns top N.
func rankClipsByText(query string, clips []artlistdb.ArtlistClip, topN int) []artlistdb.ArtlistClip {
	// Simple scoring: prefer downloaded clips, then by duration
	type scoredClip struct {
		clip  artlistdb.ArtlistClip
		score float64
	}

	var scored []scoredClip
	for _, clip := range clips {
		score := float64(clip.Duration)
		if clip.Downloaded {
			score *= 2.0 // Boost downloaded clips
		}

		// Bonus for tag matches
		for _, tag := range clip.Tags {
			if containsWord(strings.ToLower(query), strings.ToLower(tag)) {
				score += 1.0
			}
		}

		scored = append(scored, scoredClip{clip, score})
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Return top N
	if len(scored) > topN {
		scored = scored[:topN]
	}

	result := make([]artlistdb.ArtlistClip, len(scored))
	for i, sc := range scored {
		result[i] = sc.clip
	}

	return result
}

func containsWord(text, word string) bool {
	words := strings.Fields(text)
	for _, w := range words {
		if strings.ToLower(w) == word {
			return true
		}
	}
	return false
}
