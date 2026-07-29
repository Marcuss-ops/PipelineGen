// ── GET /api/clips/diagnostics + POST /api/clips/search-advanced + GET /api/clips/stats ─
//
// Diagnostics returns YouTube clip module health and dependency status.
// SearchCatalog searches the local catalog with structured filters.
// Stats returns clip statistics across all sources.

package youtube

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

type dependencyCheck struct {
	Required bool `json:"required"`
	OK       bool `json:"ok"`
}

// Diagnostics returns YouTube clip module health and dependency status.
func (h *YouTubeClipHandler) Diagnostics(c *gin.Context) {
	checks := map[string]dependencyCheck{
		"service": {Required: true, OK: h.service != nil},
		"jobs":    {Required: true, OK: h.jobsSvc != nil},
	}
	serviceAvailable := h.service != nil

	// Check external dependencies
	if serviceAvailable {
		cfg := h.service.Config()
		ytdlpPath := cfg.YtdlpPath

		// Check yt-dlp
		ytdlpOK := false
		if h.toolChecker != nil {
			_, err := h.toolChecker.LookPath(ytdlpPath)
			ytdlpOK = err == nil
		}
		checks["ytdlp"] = dependencyCheck{Required: true, OK: ytdlpOK}

		// Check ffmpeg
		ffmpegOK := false
		if h.toolChecker != nil {
			_, err := h.toolChecker.LookPath("ffmpeg")
			ffmpegOK = err == nil
		}
		checks["ffmpeg"] = dependencyCheck{Required: true, OK: ffmpegOK}

		// Check Node.js (for YouTube signature solving)
		nodeOK := false
		if h.toolChecker != nil {
			_, err := h.toolChecker.LookPath("node")
			nodeOK = err == nil
		}
		checks["node"] = dependencyCheck{Required: true, OK: nodeOK}

		// Check cookies file
		cookiesPath := cfg.YouTubeCookiesPath
		if cookiesPath == "" {
			cookiesPath = "config/youtube_cookies.txt"
		}
		_, absErr := filepath.Abs(cookiesPath)
		cookiesOK := absErr == nil && fileReadable(cookiesPath)
		checks["cookies"] = dependencyCheck{Required: false, OK: cookiesOK}

		configDetails := gin.H{
			"youtube_enabled": cfg.YouTubeEnabled,
			"extract_timeout": cfg.YouTubeExtractTimeout,
			"cookies_path":    cookiesPath,
			"ytdlp_path":      ytdlpPath,
			"js_runtime_path": cfg.YouTubeJSRuntimePath,
		}
		// Keep configuration details separate from the typed dependency checks.
		apiutil.OK(c, gin.H{"ok": requiredChecksOK(checks), "checks": checks, "config": configDetails})
		return
	}

	ok := true
	for _, check := range checks {
		if check.Required && !check.OK {
			ok = false
		}
	}
	apiutil.OK(c, gin.H{
		"ok":     ok,
		"checks": checks,
	})
}

func requiredChecksOK(checks map[string]dependencyCheck) bool {
	for _, check := range checks {
		if check.Required && !check.OK {
			return false
		}
	}
	return true
}

func fileReadable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// SearchCatalog searches the local catalog with structured filters.
func (h *YouTubeClipHandler) SearchCatalog(c *gin.Context) {
	var req asset.AdvancedSearchRequest

	// Support both GET (query params) and POST (JSON body)
	if c.Request.Method == "GET" {
		req.Q = c.Query("q")
		req.Source = c.Query("source")
		req.Category = c.Query("category")
		req.MediaType = c.Query("media_type")
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

	if h.searchSvc == nil {
		apiutil.InternalError(c, fmt.Errorf("search service not wired (composition root must populate SearchSvc)"))
		return
	}
	ctx := c.Request.Context()
	q := search.Query{
		Text:  req.Q,
		Limit: req.Limit,
		Filters: search.Filters{
			Source:    req.Source,
			MediaType: req.MediaType,
		},
		Mode: search.ParseMode(""),
	}
	if req.Source != "" && req.Source != "all" {
		q.Sources = []string{req.Source}
	}
	if req.Offset > 0 {
		q.Cursor = fmt.Sprintf("offset:%d", req.Offset)
	}
	res, err := h.searchSvc.Search(ctx, q)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("search.Aggregate: %w", err))
		return
	}
	clips := make([]gin.H, 0, len(res.Items))
	for _, item := range res.Items {
		clips = append(clips, gin.H{
			"id":           item.SourceRef,
			"title":        item.Title,
			"source_name":  item.Source,
			"score":        item.Score,
			"thumb_url":    item.ThumbnailURL,
			"preview_url":  item.PreviewURL,
			"media_type":   item.MediaType,
			"qdrant_score": item.Score,
			"rerank_score": item.Score,
		})
	}
	apiutil.OK(c, gin.H{
		"ok":              true,
		"count":           len(clips),
		"total":           len(res.Items),
		"clips":           clips,
		"cursor":          res.NextCursor,
		"provider_errors": res.ProviderErrors,
	})
}

// Stats returns clip statistics across all sources.
func (h *YouTubeClipHandler) Stats(c *gin.Context) {
	if h.searchFanOut == nil {
		apiutil.InternalError(c, fmt.Errorf("search fan-out not wired"))
		return
	}
	stats := h.searchFanOut.Stats()
	providers := make(map[string]gin.H, len(stats))
	for name, s := range stats {
		providers[name] = gin.H{
			"hits":           s.Hits,
			"calls":          s.Calls,
			"errors":         s.Errors,
			"avg_latency_ms": s.AverageLatency().Milliseconds(),
		}
	}

	apiutil.OK(c, gin.H{
		"ok":        true,
		"providers": providers,
	})
}
