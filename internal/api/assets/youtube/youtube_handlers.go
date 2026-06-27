// Package youtube hosts the HTTP handlers for the YouTube clip download,
// info, extract, search, and diagnostics endpoints. Split out from the
// now-deleted internal/api/sources/ package (PR-A consolidation)
// YouTube transport isolated from the rest of the SourcesHandler.
//
// The clips repository is injected at construction time so the handler has
// no late-binding setters.
package youtube

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	ytports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	yttypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
	"github.com/Marcuss-ops/PipelineGen/internal/api/common"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// YouTubeClipHandler owns the HTTP transport for YouTube clip operations:
// download, info, advanced search, diagnostics, and stats. Construction
// mirrors the legacy api/sources package, but lives here (in package
// youtube) so sub-handlers can be tested in isolation.
//
// PR8 (June 2026): added Idempotency field — the reusable Gin
// idempotency middleware instance installed on POST /clips/process
// (the only Write route in this handler). Read routes fall through.
type YouTubeClipHandler struct {
	service         *youtube.Service
	log             *zap.Logger
	jobsSvc         jobservice.Service
	clipsRepo       ytports.ClipStorePort
	providerSearch  providers.SearchProvider
	providerReg     *providers.Registry
	providerResolve sync.Once
	toolChecker     appassets.ToolChecker
	Idempotency     gin.HandlerFunc
}

// NewYouTubeClipHandler builds the YouTubeClipHandler.
//
//	service          - YouTube service used by this handler.
//	log              - zap logger for diagnostics.
//	jobsSvc          - job system used by the async extract endpoint.
//	providerRegistry - providers.Registry for search dispatch (nil = legacy path).
//	                    Resolved lazily on first SearchTopics call so providers
//	                    registered after construction are still discovered.
//
// PR8 (June 2026): added idempotencyMiddleware to wrap POST /clips/process
// (the only Write route in the handler). Read routes (info, search,
// diagnostics, stats) are unchanged. nil disables idempotency for
// test fixtures.
func NewYouTubeClipHandler(service *youtube.Service, log *zap.Logger, jobsSvc jobservice.Service, providerRegistry *providers.Registry, clipsRepo ytports.ClipStorePort, toolChecker appassets.ToolChecker, idempotencyMiddleware gin.HandlerFunc) *YouTubeClipHandler {
	var idem gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if idempotencyMiddleware != nil {
		idem = idempotencyMiddleware
	}
	return &YouTubeClipHandler{
		service:     service,
		log:         log,
		jobsSvc:     jobsSvc,
		clipsRepo:   clipsRepo,
		providerReg: providerRegistry,
		toolChecker: toolChecker,
		Idempotency: idem,
	}
}

// resolveProvider lazily resolves the YouTube SearchProvider from the
// registry on first call. Thread-safe via sync.Once so concurrent
// SearchTopics invocations see a consistent result. No-op when the
// handler was constructed without a registry (legacy direct path).
func (h *YouTubeClipHandler) resolveProvider() {
	if h.providerReg == nil {
		return
	}
	h.providerResolve.Do(func() {
		p, ok := h.providerReg.Get("youtube")
		if !ok || p == nil {
			return
		}
		if sp, ok := p.(providers.SearchProvider); ok {
			h.providerSearch = sp
		}
	})
}

// RegisterRoutes wires the YouTube clip endpoints onto the supplied
// gin router group. Mounts on /api/media/clips/* in production.
//
// PR8 (June 2026): POST /process (the YouTube clip extraction job
// enqueue endpoint) installs h.Idempotency so Idempotency-Key replay
// works across retry storms. Read routes (info, search, diagnostics,
// stats) fall through unchanged.
func (h *YouTubeClipHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/process", h.Idempotency, h.Extract)
	r.GET("/info", h.GetVideoInfo)
	r.GET("/search", h.SearchAdvanced)
	r.POST("/search", h.SearchAdvanced)
	r.GET("/diagnostics", h.Diagnostics)
	r.GET("/stats", h.Stats)
}

// Wave 16 PR1 (June 2026): SearchTopics + searchTopicsViaProvider +
// providersToTopicResults removed — canonical search is
// SearchAdvanced via GET/POST /api/media/clips/search. See
// architecture/deprecations.yaml#PR-YT-SEARCHTOPICS for the removal
// record (deprecation ID + owner_capability + replacement +
// introduction_date + removal_date + tracking_issue + compatibility_test +
// usage_metric + status).
//
// The provider-registry wiring (resolveProvider + providerSearch field +
// providerReg field + providerResolve sync.Once) is preserved for the
// follow-up PR-CLIP-YT-REGISTRY-CLEANUP (separate commit) that
// collapses it in a single archaeology pass against the one provider
// wiring site (internal/app/clips_adapters_index.go) and the constructor
// parameter on NewYouTubeClipHandler. Per godlike/07 §"Migration sequence"
// this PR lands only the method-removal phase; the field/parameter
// collapse ships with PR-CLIP-YT-REGISTRY-CLEANUP so the diff stays
// archaeological and review-friendly.

// GetVideoInfo returns metadata for a single YouTube URL.
func (h *YouTubeClipHandler) GetVideoInfo(c *gin.Context) {
	url := c.Query("url")
	if url == "" {
		apiutil.BadRequest(c, "url parameter is required")
		return
	}

	metadata, err := h.service.GetVideoInfo(c.Request.Context(), url)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, metadata)
}

// Extract enqueues a YouTube clip extraction job. When Destination.Group
// is set the caller's root folder is rewritten to a per-group channel
// subfolder so clips land in Root/<Group>/video-title/.
func (h *YouTubeClipHandler) Extract(c *gin.Context) {
	req, ok := apiutil.BindJSON[yttypes.ExtractRequest](c)
	if !ok {
		return
	}

	// ── Pre-resolve per-Group (channel) Drive subfolder ──────────────
	// When the caller provides a root folder_id and a group name,
	// look up or create the channel subfolder and use its ID instead
	// of the root. This only runs in the HTTP handler path (direct API
	// calls). The monitor pre-resolves separately in downloadClip()
	// before calling Extract directly on the YouTube service.
	if req.Destination != nil && req.Destination.FolderID != "" && req.Destination.Group != "" {
		channelFolderID, err := h.service.GetOrCreateChannelFolder(c.Request.Context(), req.Destination.Group, req.Destination.FolderID)
		if err == nil && channelFolderID != req.Destination.FolderID {
			h.log.Info("pre-resolved channel Drive subfolder from API request",
				zap.String("group", req.Destination.Group),
				zap.String("root_folder", req.Destination.FolderID),
				zap.String("channel_folder_id", channelFolderID))
			req.Destination.FolderID = channelFolderID
		}
	}

	payloadBytes, err := json.Marshal(req)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to marshal request: %w", err))
		return
	}
	var payloadMap map[string]any
	if err := json.Unmarshal(payloadBytes, &payloadMap); err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to prepare payload: %w", err))
		return
	}

	if ok := common.EnqueueAsync(c, h.jobsSvc, &common.EnqueueInput{
		Type:    "youtube_clip.extract",
		Payload: payloadMap,
	}, "YouTube clip extraction job enqueued."); ok {
		return
	}
	// EnqueueAsync returns false if jobsSvc is nil (503) or on error.
}

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

	// Search across all clip repositories
	ctx := c.Request.Context()
	repos := h.getAllClipRepos()
	var allClips []*asset.Asset
	total := 0

	for source, repo := range repos {
		sourceReq := req
		if sourceReq.Source == "" || sourceReq.Source == "all" {
			sourceReq.Source = "" // search all
		} else if sourceReq.Source != source {
			continue
		}

		result, err := repo.SearchClipsAdvanced(ctx, sourceReq)
		if err != nil {
			h.log.Warn("search failed for source", zap.String("source", source), zap.Error(err))
			continue
		}
		allClips = append(allClips, result.Clips...)
		total += result.Total
	}

	// Apply limit across all results
	if req.Limit > 0 && len(allClips) > req.Limit {
		allClips = allClips[:req.Limit]
	}

	apiutil.OK(c, gin.H{
		"ok":    true,
		"count": len(allClips),
		"total": total,
		"clips": allClips,
	})
}

// Stats returns clip statistics across all sources.
func (h *YouTubeClipHandler) Stats(c *gin.Context) {
	ctx := c.Request.Context()
	repos := h.getAllClipRepos()

	stats := make(map[string]int)
	totalClips := 0

	for source, repo := range repos {
		count, err := repo.CountClips(ctx)
		if err != nil {
			h.log.Warn("failed to count clips", zap.String("source", source), zap.Error(err))
			continue
		}
		stats[source] = count
		totalClips += count
	}

	apiutil.OK(c, gin.H{
		"ok":        true,
		"total":     totalClips,
		"by_source": stats,
	})
}

// getAllClipRepos returns all available clip repositories keyed by source.
// Currently only YouTube has a registered clips repo; other sources can be
// added here once their repo wiring is migrated.
func (h *YouTubeClipHandler) getAllClipRepos() map[string]ytports.ClipStorePort {
	repos := make(map[string]ytports.ClipStorePort)
	if h.clipsRepo != nil {
		repos["youtube"] = h.clipsRepo
	}
	return repos
}
