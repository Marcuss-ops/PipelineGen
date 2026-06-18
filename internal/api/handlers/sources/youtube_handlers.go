package sources

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

type YouTubeClipHandler struct {
	service   *youtube.Service
	log       *zap.Logger
	jobsSvc   *jobservice.Service
	clipsRepo *clips.Repository
}

func NewYouTubeClipHandler(service *youtube.Service, log *zap.Logger, jobsSvc *jobservice.Service) *YouTubeClipHandler {
	return &YouTubeClipHandler{
		service: service,
		log:     log,
		jobsSvc: jobsSvc,
	}
}

// SetClipsRepo sets the clips repository for advanced search.
func (h *YouTubeClipHandler) SetClipsRepo(repo *clips.Repository) {
	h.clipsRepo = repo
}

func (h *YouTubeClipHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/process", h.Extract)
	r.GET("/info", h.GetVideoInfo)
	r.GET("/search", h.SearchAdvanced)
	r.POST("/search", h.SearchAdvanced)
	r.GET("/diagnostics", h.Diagnostics)
	r.GET("/stats", h.Stats)
}

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

	resp, err := h.service.SearchTopicVideos(c.Request.Context(), req.Q, req.Limit, req.Sort)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, resp)
}

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

func (h *YouTubeClipHandler) Extract(c *gin.Context) {
	req, ok := apiutil.BindJSON[youtube.ExtractRequest](c)
	if !ok {
		return
	}

	// ── Pre-resolve per-Group (channel) Drive subfolder ──────────────
	// When the caller provides a root folder_id and a group name
	// (e.g. folder_id="ComedyRoot", group="AmeliaDimoldenberg"), look up
	// or create the channel subfolder and use its ID instead of the root.
	// This ensures clips go into Root/AmeliaDimoldenberg/video-title/
	// instead of flat Root/video-title/.
	//
	// This only runs in the HTTP handler path (direct API calls).
	// The monitor pre-resolves separately in downloadClip() before
	// calling Extract directly on the service.
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
			Type:    models.JobTypeYouTubeClipExtract,
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
			if _, err := exec.LookPath(ytdlpPath); err != nil {
				checks["ytdlp"] = "not_found"
			} else {
				checks["ytdlp"] = "ok"
			}

			// Check ffmpeg
			if _, err := exec.LookPath("ffmpeg"); err != nil {
				checks["ffmpeg"] = "not_found"
			} else {
				checks["ffmpeg"] = "ok"
			}

			// Check Node.js (for YouTube signature solving)
			if _, err := exec.LookPath("node"); err != nil {
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
	var req clips.AdvancedSearchRequest

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
	var allClips []*models.MediaAsset
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

// getAllClipRepos returns all available clip repositories.
func (h *YouTubeClipHandler) getAllClipRepos() map[string]*clips.Repository {
	repos := make(map[string]*clips.Repository)
	if h.clipsRepo != nil {
		repos["youtube"] = h.clipsRepo
	}
	return repos
}
