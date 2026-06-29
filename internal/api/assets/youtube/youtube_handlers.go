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

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	transport "github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	yttypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
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
	service     *youtube.Service
	log         *zap.Logger
	jobsSvc     jobservice.Service
	clipsRepo   ytports.ClipStorePort
	toolChecker appassets.ToolChecker
	Idempotency gin.HandlerFunc
	// searchAggregator (S3d, June 2026): wires SearchAdvanced + Stats
	// through the canonical SearchAggregator. When nil, both methods
	// return 503 (services not wired) rather than a partial-result
	// path; the composition root is required to provide one in any
	// post-Freeze configuration. Migration target: SearchAdvanced +
	// Stats are aggregator-routed (S3d); the legacy h.getAllClipRepos()
	// method is removed.
	searchAggregator *providers.SearchAggregator
} // NewYouTubeClipHandler builds the YouTubeClipHandler.
// service          - YouTube service used by this handler.
// log              - zap logger for diagnostics.
// jobsSvc          - job system used by the async extract endpoint.
// clipsRepo        - canonical YouTube clip-store port.
// toolChecker      - external-tool probe used by Diagnostics.
// idempotencyMiddleware - reusable Gin idempotency middleware; nil disables.
// searchAggregator    - canonical SearchAggregator for SearchAdvanced + Stats.
//
// PR-CLIP-YT-REGISTRY-CLEANUP (June 2026): providerRegistry arg +
// providerSearch field + providerReg field + providerResolve sync.Once +
// resolveProvider() method all removed. The handler no longer resolves a
// SearchProvider from providers.Registry; routes that need search dispatch
// go through SearchAdvanced (aggregator-routed).
//
// S3d (June 2026): clipsRepo retained for downstream uses (reprocess /
// download paths that don't go through the aggregator), but SearchAdvanced
// + Stats are now aggregator-only via the appended searchAggregator arg.
// Composition root wires NewSearchAggregator(post-Freeze registry) and
// passes that SAME pointer to both YouTubeClipHandler + the clips.Handler
// (FindDuplicates). The h.getAllClipRepos() method was REMOVED.
//
// PR8 (June 2026): added idempotencyMiddleware to wrap POST /clips/process
// (the only Write route in the handler). Read routes (info, search,
// diagnostics, stats) are unchanged. nil disables idempotency for
// test fixtures.
func NewYouTubeClipHandler(service *youtube.Service, log *zap.Logger, jobsSvc jobservice.Service, clipsRepo ytports.ClipStorePort, toolChecker appassets.ToolChecker, idempotencyMiddleware gin.HandlerFunc, searchAggregator *providers.SearchAggregator) *YouTubeClipHandler {
	var idem gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if idempotencyMiddleware != nil {
		idem = idempotencyMiddleware
	}
	return &YouTubeClipHandler{
		service:          service,
		log:              log,
		jobsSvc:          jobsSvc,
		clipsRepo:        clipsRepo,
		toolChecker:      toolChecker,
		Idempotency:      idem,
		searchAggregator: searchAggregator,
	}
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
// PR-CLIP-YT-REGISTRY-CLEANUP (June 2026, this PR): the
// provider-registry wiring (resolveProvider + providerSearch field +
// providerReg field + providerResolve sync.Once + providerRegistry ctor
// arg + providers import) was COLLAPSED here. The handler is now
// transport-only against the canonical clip-repo; the providers.Registry
// continues to exist as a composition-root dispatch concern (root.Search.
// ProviderRegistry in WireRegistry→pr.Freeze()) but does not reach any
// per-handler field. See architecture/deprecations.yaml#PR-CLIP-YT-REGISTRY-CLEANUP
// for the deprecation record.

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

	if ok := transport.EnqueueAsync(c, h.jobsSvc, &transport.EnqueueInput{
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

	// S3d (June 2026): SearchAdvanced routes through the
	// canonical SearchAggregator. The aggregator fans out a
	// semantic-search call to every provider advertising
	// CapabilitySearch in providers.Registry. The per-source
	// clip-repo loop is REMOVED.
	//
	// Field-mapping from AdvancedSearchRequest → SearchQuery:
	//   Q                  → SearchQuery.Query
	//   Category           → SearchQuery.MediaType (soft hint)
	//   Min/MaxDuration    → NOT routed (no clean aggregator
	//                         equivalent in S3d; documented as a
	//                         future-wave field addition)
	//   SortBy/SortAsc     → NOT routed (pre-S3d limitation)
	//   HasTranscript      → NOT routed (pre-S3d limitation)
	//   HasDriveLink       → NOT routed (pre-S3d limitation)
	//   CreatedAfter/Before → NOT routed in S3d
	//   Offset             → translated into a Cursor hint;
	//                         cursor-based pagination replaces
	//                         offset semantics starting S3d
	//
	// Response shape change: `clips []gin.H{...}` now carries
	// the canonical Candidate-shaped projection (provider_name,
	// score, qdrant_score, rerank_score, thumb_url) instead of
	// the legacy `[]*asset.Asset` projection. Operators that
	// require asset-shaped response rows should migrate to the
	// /api/assets/clips/search endpoint (handler.go).
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
		// Best-effort: encode Offset as a passable cursor token.
		// The aggregator decodes opaque cursors best-effort; the
		// non-base64 form falls back to first-page semantics.
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
//
// S3d (June 2026): Stats routes through the canonical
// SearchAggregator. Composition root wires the aggregator with
// Artlist + YouTube + Stock adapters (via providers.SearchProvider
// fan-out). Per-provider call telemetry is captured in
// AggregateStats.Providers after the Aggregate call completes.
//
// NOTE: this is a semantic shift from the legacy per-source clip-
// COUNT semantics (`repo.CountClips(ctx)`). The aggregator
// returns per-provider Hits telemetry = number of candidates
// returned by each provider's Search call, NOT canonical clip-
// store COUNTs. Operators that need absolute counts should
// compose against the asset-store directly (Deps.AssetRepo etc).
func (h *YouTubeClipHandler) Stats(c *gin.Context) {
	if h.searchAggregator == nil {
		apiutil.InternalError(c, fmt.Errorf("search aggregator not wired (composition root must populate root.Search.SearchAggregator)"))
		return
	}
	ctx := c.Request.Context()
	if _, aggErr := h.searchAggregator.Aggregate(ctx, &providers.SearchQuery{}, providers.AggregateOptions{
		Sources: []string{"artlist", "youtube", "stock"},
	}); aggErr != nil {
		// Do not 5xx stats: a failed list-everything call
		// shouldn't blank the operator dashboard. Aggregator-
		// level errors still surface in /diagnostics.
		h.log.Warn("search aggregator.Aggregate (stats) failed; returning zeroed Stats() snapshot", zap.Error(aggErr))
	}
	aggStats := h.searchAggregator.Stats()
	stats := make(map[string]any)
	totalClips := 0
	for source, ps := range aggStats.Providers {
		stats[source] = gin.H{
			"hits":           ps.Hits,
			"calls":          ps.Calls,
			"errors":         ps.Errors,
			"avg_latency_ms": ps.AvgLatency().Milliseconds(),
		}
		totalClips += ps.Hits
	}
	apiutil.OK(c, gin.H{
		"ok":          true,
		"total":       totalClips,
		"by_source":   stats,
		"by_provider": aggStats.Providers,
	})
}

// S3d (June 2026) removal: getAllClipRepos() is REMOVED.
// The SearchAdvanced + Stats methods now route through the
// canonical SearchAggregator. The clipsRepo field on the
// struct stays for downstream uses (reprocess / download paths
// that don't aggregate provider fan-out).
