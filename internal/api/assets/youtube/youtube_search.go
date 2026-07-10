// ── GET /api/clips/diagnostics + POST /api/clips/search-advanced + GET /api/clips/stats ─
//
// Diagnostics returns YouTube clip module health and dependency status.
// SearchAdvanced performs advanced clip search with structured filters.
// Stats returns clip statistics across all sources.

package youtube

import (
	"fmt"
	"path/filepath"

	"github.com/gin-gonic/gin"

	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Diagnostics returns YouTube clip module health and dependency status.
func (h *YouTubeClipHandler) Diagnostics(c *gin.Context) {
	serviceAvailable := h.service != nil
	jobsAvailable := h.jobsSvc != nil

	checks := gin.H{
		"service": serviceAvailable,
		"jobs":    jobsAvailable,
	}

	// Check external dependencies
	if serviceAvailable {
		cfg := h.service.Config()
		ytdlpPath := cfg.YtdlpPath

		// Check yt-dlp
		if _, err := h.toolChecker.LookPath(ytdlpPath); err != nil {
			checks["ytdlp"] = "not_found"
		} else {
			checks["ytdlp"] = "ok"
		}

		// Check ffmpeg
		if _, err := h.toolChecker.LookPath("ffmpeg"); err != nil {
			checks["ffmpeg"] = "not_found"
		} else {
			checks["ffmpeg"] = "ok"
		}

		// Check Node.js (for YouTube signature solving)
		if _, err := h.toolChecker.LookPath("node"); err != nil {
			checks["node"] = "not_found"
		} else {
			checks["node"] = "ok"
		}

		// Check cookies file
		cookiesPath := cfg.YouTubeCookiesPath
		if cookiesPath == "" {
			cookiesPath = "config/youtube_cookies.txt"
		}
		if _, err := filepath.Abs(cookiesPath); err != nil {
			checks["cookies"] = "invalid_path"
		} else {
			checks["cookies"] = "configured"
		}

		checks["config"] = gin.H{
			"youtube_enabled": cfg.YouTubeEnabled,
			"extract_timeout": cfg.YouTubeExtractTimeout,
			"cookies_path":    cookiesPath,
			"ytdlp_path":      ytdlpPath,
			"js_runtime_path": cfg.YouTubeJSRuntimePath,
		}
	}

	apiutil.OK(c, gin.H{
		"ok":     serviceAvailable && jobsAvailable,
		"checks": checks,
	})
}

// SearchAdvanced performs advanced clip search with structured filters.
func (h *YouTubeClipHandler) SearchAdvanced(c *gin.Context) {
	var req asset.AdvancedSearchRequest

	// Support both GET (query params) and POST (JSON body)
	if c.Request.Method == "GET" {
		req.Q = c.Query("q")
		req.Source = c.Query("source")
		req.Category = c.Query("category")
		req.SortBy = c.Query("sort_by")
		req.CreatedAfter = c.Query("created_after")
		req.CreatedBefore = c.Query("created_before")
		fmt.Sscanf(c.DefaultQuery("min_duration", "0"), "%d", &req.MinDuration)
		fmt.Sscanf(c.DefaultQuery("max_duration", "0"), "%d", &req.MaxDuration)
		fmt.Sscanf(c.DefaultQuery("limit", "50"), "%d", &req.Limit)
		fmt.Sscanf(c.DefaultQuery("offset", "0"), "%d", &req.Offset)
		req.HasTranscript = c.Query("has_transcript") == "true"
		req.HasDriveLink = c.Query("has_drive_link") == "true"
		req.SortAsc = c.Query("sort_asc") == "true"
	} else {
		if err := c.ShouldBindJSON(&req); err != nil {
			apiutil.BadRequest(c, err.Error())
			return
		}
	}

	if h.searchAggregator == nil {
		apiutil.InternalError(c, fmt.Errorf("search aggregator not wired (composition root must populate root.Search.SearchAggregator)"))
		return
	}
	ctx := c.Request.Context()
	sources := []string{"artlist", "youtube", "stock"}
	if req.Source != "" && req.Source != "all" {
		sources = []string{req.Source}
	}
	cursor := ""
	if req.Offset > 0 {
		cursor = fmt.Sprintf("offset:%d", req.Offset)
	}
	aggRes, aggErr := h.searchAggregator.Aggregate(ctx, &providers.SearchQuery{
		Query:     req.Q,
		MediaType: req.Category,
	}, providers.AggregateOptions{
		Limit:   req.Limit,
		Cursor:  cursor,
		Sources: sources,
	})
	if aggErr != nil {
		apiutil.InternalError(c, fmt.Errorf("aggregator.Aggregate: %w", aggErr))
		return
	}
	clips := make([]gin.H, 0, len(aggRes.Hits))
	for _, hit := range aggRes.Hits {
		clips = append(clips, gin.H{
			"id":           hit.Candidate.SourceRef,
			"title":        hit.Candidate.Title,
			"source_name":  hit.ProviderName,
			"score":        hit.FinalScore,
			"thumb_url":    hit.Candidate.ThumbnailURL,
			"preview_url":  hit.Candidate.PreviewURL,
			"media_type":   string(hit.Candidate.MediaType),
			"qdrant_score": hit.QdrantScore,
			"rerank_score": hit.RerankScore,
		})
	}
	apiutil.OK(c, gin.H{
		"ok":              true,
		"count":           len(clips),
		"total":           aggRes.Total,
		"clips":           clips,
		"cursor":          aggRes.Cursor,
		"provider_errors": aggRes.ProviderErrors,
	})
}

// Stats returns clip statistics across all sources.
func (h *YouTubeClipHandler) Stats(c *gin.Context) {
	if h.searchAggregator == nil {
		apiutil.InternalError(c, fmt.Errorf("search aggregator not wired"))
		return
	}
	ctx := c.Request.Context()
	sources := []string{"artlist", "youtube", "stock"}

	aggRes, aggErr := h.searchAggregator.Aggregate(ctx, &providers.SearchQuery{}, providers.AggregateOptions{
		Limit:   1,
		Sources: sources,
	})
	if aggErr != nil {
		apiutil.InternalError(c, fmt.Errorf("aggregator.Aggregate: %w", aggErr))
		return
	}

	apiutil.OK(c, gin.H{
		"ok":              true,
		"total_candidates": aggRes.Total,
		"providers":        aggRes.ProviderErrors,
	})
}
