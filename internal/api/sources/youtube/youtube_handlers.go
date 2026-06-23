// Package youtube hosts the HTTP handlers for the YouTube clip download,
// info, extract, search, and diagnostics endpoints. Split out from the
// legacy flat internal/api/sources/ package as part of PR-A to keep the
// YouTube transport isolated from the rest of the SourcesHandler.
//
// All handlers share the same SetClipsRepo(...) injection pattern as the
// legacy file: the clip-repository is wired in from the registry after
// the handler is constructed.
package youtube

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	executil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// YouTubeClipHandler owns the HTTP transport for YouTube clip operations:
// download, info, advanced search, diagnostics, and stats. Construction
// mirrors the legacy api/sources package, but lives here (in package
// youtube) so sub-handlers can be tested in isolation.
type YouTubeClipHandler struct {
	service        *youtube.Service
	log            *zap.Logger
	jobsSvc        *jobservice.Service
	clipsRepo      *assets.ClipsRepository
	providerSearch providers.SearchProvider
}

// NewYouTubeClipHandler builds the YouTubeClipHandler.
//
//	service - YouTube service used by this handler.
//	log     - zap logger for diagnostics.
//	jobsSvc - job system used by the async extract endpoint.
func NewYouTubeClipHandler(service *youtube.Service, log *zap.Logger, jobsSvc *jobservice.Service) *YouTubeClipHandler {
	return &YouTubeClipHandler{
		service: service,
		log:     log,
		jobsSvc: jobsSvc,
	}
}

// SetClipsRepo sets the clips repository for advanced search.
func (h *YouTubeClipHandler) SetClipsRepo(repo *assets.ClipsRepository) {
	h.clipsRepo = repo
}

// SetProviderRegistry resolves the canonical YouTube provider from the
// frozen providers.Registry and stashes its SearchProvider handle on
// the handler. Once set, SearchTopics routes through the registry's
// adapter rather than calling *youtube.Service directly — this advances
// the cut-over recipe step 1 in docs/migration-maps/internal-sources.md
// ("finish provider-registry migration"). Nil or registry-without-youtube
// leaves the handler on the legacy direct path so existing tests keep
// working unchanged.
func (h *YouTubeClipHandler) SetProviderRegistry(reg *providers.Registry) {
	if reg == nil {
		return
	}
	p, ok := reg.Get("youtube")
	if !ok || p == nil {
		return
	}
	if sp, ok := p.(providers.SearchProvider); ok {
		h.providerSearch = sp
	}
}

// RegisterRoutes wires the YouTube clip endpoints onto the supplied
// gin router group. Mounts on /api/media/clips/* in production.
func (h *YouTubeClipHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/process", h.Extract)
	r.GET("/info", h.GetVideoInfo)
	r.GET("/search", h.SearchAdvanced)
	r.POST("/search", h.SearchAdvanced)
	r.GET("/diagnostics", h.Diagnostics)
	r.GET("/stats", h.Stats)
}

// SearchTopics topic-search endpoint, kept for backward compatibility
// (deprecated in favor of YouTubeClipHandler.SearchAdvanced).
//
// Cut-over recipe step 1 (docs/migration-maps/internal-sources.md):
// when SetProviderRegistry has bound a SearchProvider handle on the
// handler (the production wiring path through providers.Registry), this
// route dispatches via the adapter so the response shape originates
// from the canonical providers package. When no provider is bound
// (e.g. unit tests that don't exercise the registry, or stub wiring),
// we fall back to the legacy *youtube.Service direct call — the JSON
// shape is preserved in both paths.
func (h *YouTubeClipHandler) SearchTopics(c *gin.Context) {
	var req youtube.TopicSearchRequest
	if err := c.ShouldBind(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	if req.Q == "" {
		apiutil.BadRequest(c, "q parameter is required")
		return
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}
	if req.Limit > 50 {
		req.Limit = 50
	}

	if h.providerSearch != nil {
		h.searchTopicsViaProvider(c, &req)
		return
	}

	// PR-3F: SearchTopicVideos wrapper was deleted; route through the
	// single canonical SearchByTopicWithFilter entry point.
	resp, err := h.service.SearchByTopicWithFilter(c.Request.Context(), req.Q, req.Limit, req.Sort, "")
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, resp)
}

// searchTopicsViaProvider dispatches the topic search through the
// injected SearchProvider and translates the canonical
// providers.SearchResult back into the legacy youtube.TopicSearchResponse
// shape so the API contract is preserved for both code paths.
func (h *YouTubeClipHandler) searchTopicsViaProvider(c *gin.Context, req *youtube.TopicSearchRequest) {
	searchReq := providers.SearchRequest{
		Query:     req.Q,
		Limit:     req.Limit,
		TopicOnly: true, // YouTube adapter prefers SearchByTopicWithFilter
	}
	if req.Sort != "" {
		searchReq.Filters.Sort = providers.SortMode(req.Sort)
	}

	res, err := h.providerSearch.Search(c.Request.Context(), searchReq)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("youtube provider search: %w", err))
		return
	}

	apiutil.OK(c, &youtube.TopicSearchResponse{
		OK:      true,
		Query:   req.Q,
		Limit:   req.Limit,
		Count:   len(res.Candidates),
		Source:  "youtube_live", // preserved verbatim from legacy path for HTTP-client compat
		Results: providersToTopicResults(res.Candidates),
	})
}

// providersToTopicResults translates the canonical providers.Candidate
// shape back into the legacy youtube.TopicSearchResult JSON.
//
// IMPORTANT — LOSSY TRANSLATION
// The Provider boundary intentionally drops the YouTube-specific
// per-dimension metadata (channel name, view count, separate topic
// similarity vs format-match scores — they belong to a YouTube view
// layer the Adapter is meant to abstract away). Consumers reading
// ChannelName and ViewCount will always see "" / 0 on the provider
// path; consumers reading SimilarityScore and FormatMatchPercent
// will see the same combined percentage on both fields rather than
// the legacy 70/30 split (the adapter collapses them into a single
// Score). If a downstream consumer depends on the per-dimension
// values, route the search through the legacy direct call instead
// (don't bind the ProviderRegistry on the handler in that path).
func providersToTopicResults(candidates []providers.Candidate) []youtube.TopicSearchResult {
	out := make([]youtube.TopicSearchResult, 0, len(candidates))
	for _, c := range candidates {
		r := youtube.TopicSearchResult{
			VideoID:      c.SourceRef,
			Title:        c.Title,
			ThumbnailURL: c.ThumbnailURL,
			DirectLink:   c.PreviewURL,
			Duration:     c.Duration.Seconds(),
		}
		if c.PublishedAt != nil {
			r.UploadDate = c.PublishedAt.UTC().Format("20060102")
		}
		// Adapter combines legacy (similarity*70 + format*30)/10000
		// into the single Candidate.Score ∈ [0,1]. The Provider
		// boundary does NOT carry the per-dimension split, so we
		// publish the combined percentage on BOTH legacy fields.
		// summary: similarity == format always on the provider path.
		score100 := int(c.Score * 100)
		if score100 > 100 {
			score100 = 100
		}
		if score100 < 0 {
			score100 = 0
		}
		r.SimilarityScore = score100
		r.FormatMatchPercent = score100
		out = append(out, r)
	}
	return out
}

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
	req, ok := apiutil.BindJSON[youtube.ExtractRequest](c)
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

	if h.jobsSvc != nil {
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

		job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
			Type:    "youtube_clip.extract",
			Payload: payloadMap,
		})
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to enqueue job: %w", err))
			return
		}

		apiutil.OK(c, gin.H{
			"job_id":     job.ID,
			"message":    "YouTube clip extraction job enqueued",
			"status_url": "/api/jobs/" + job.ID + "/full",
		})
		return
	}

	apiutil.InternalError(c, fmt.Errorf("jobs service not available"))
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
		if cfg := h.service.Config(); cfg != nil {
			ytdlpPath := cfg.External.ResolvedYtdlpPath()

			// Check yt-dlp
			if _, err := executil.LookPath(ytdlpPath); err != nil {
				checks["ytdlp"] = "not_found"
			} else {
				checks["ytdlp"] = "ok"
			}

			// Check ffmpeg
			if _, err := executil.LookPath("ffmpeg"); err != nil {
				checks["ffmpeg"] = "not_found"
			} else {
				checks["ffmpeg"] = "ok"
			}

			// Check Node.js (for YouTube signature solving)
			if _, err := executil.LookPath("node"); err != nil {
				checks["node"] = "not_found"
			} else {
				checks["node"] = "ok"
			}

			// Check cookies file
			cookiesPath := cfg.External.YouTubeCookiesPath
			if cookiesPath == "" {
				cookiesPath = "config/youtube_cookies.txt"
			}
			if _, err := filepath.Abs(cookiesPath); err != nil {
				checks["cookies"] = "invalid_path"
			} else {
				checks["cookies"] = "configured"
			}

			checks["config"] = gin.H{
				"youtube_enabled": cfg.Features.YouTubeEnabled,
				"extract_timeout": cfg.Jobs.YouTubeExtractTimeout,
				"cookies_path":    cookiesPath,
				"ytdlp_path":      ytdlpPath,
				"js_runtime_path": cfg.External.YouTubeJSRuntimePath,
			}
		}
	}

	apiutil.OK(c, gin.H{
		"ok":     serviceAvailable && jobsAvailable,
		"checks": checks,
	})
}

// SearchAdvanced performs advanced clip search with structured filters.
func (h *YouTubeClipHandler) SearchAdvanced(c *gin.Context) {
	var req assets.AdvancedSearchRequest

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
func (h *YouTubeClipHandler) getAllClipRepos() map[string]*assets.ClipsRepository {
	repos := make(map[string]*assets.ClipsRepository)
	if h.clipsRepo != nil {
		repos["youtube"] = h.clipsRepo
	}
	return repos
}
