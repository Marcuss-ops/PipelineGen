package sources

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/api/middleware"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/catalogsync"
	"velox/go-master/internal/service/clipresolver"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
)

type ArtlistHandler struct {
	service        *artlist.Service
	catalogSync    *catalogsync.Service
	jobsService    *jobservice.Service
	clipResolver   *clipresolver.Service
	nodeScraperDir string
	log            *zap.Logger
	presetsConfig  *artlist.PresetsConfig
	cfg            *config.Config
}

func NewArtlistHandler(
	service *artlist.Service,
	catalogSync *catalogsync.Service,
	jobsService *jobservice.Service,
	clipResolver *clipresolver.Service,
	nodeScraperDir string,
	log *zap.Logger,
	presetsConfig *artlist.PresetsConfig,
	cfg *config.Config,
) *ArtlistHandler {
	return &ArtlistHandler{
		service:        service,
		catalogSync:    catalogSync,
		jobsService:    jobsService,
		clipResolver:   clipResolver,
		nodeScraperDir: nodeScraperDir,
		log:            log,
		presetsConfig:  presetsConfig,
		cfg:            cfg,
	}
}

func (h *ArtlistHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering Artlist routes")

	// Public routes
	r.POST("/run", h.RunTagPipeline)
	r.POST("/run-smart", h.RunSmartPipeline)
	r.GET("/runs/:run_id", h.RunStatus)
	r.GET("/stats", h.Stats)
	r.GET("/diagnostics", h.Diagnostics)

	// Internal routes (require X-Internal or X-Velox-Internal header)
	internal := r.Group("", middleware.RequireInternalHeader())
	internal.POST("/search", h.Search)
	internal.POST("/search/live", h.SearchLive)
	internal.POST("/recommend", h.Recommend)
	internal.POST("/sync-catalogs", h.SyncCatalogs)
}

// RunTagPipeline executes the full Artlist flow for a tag
func (h *ArtlistHandler) RunTagPipeline(c *gin.Context) {
	req, ok := apiutil.BindJSON[artlist.RunTagRequest](c)
	if !ok {
		return
	}

	if strings.TrimSpace(req.Term) == "" {
		apiutil.BadRequest(c, "term is required")
		return
	}

	// Normalize request before enqueue
	req = artlist.NormalizeRunTagRequest(req, artlist.RunDefaults{
		DefaultRootFolderID: h.cfg.Harvester.DriveFolderID,
		MaxLimit:            500,
	})

	h.log.Info("artlist run requested",
		zap.String("term", req.Term),
		zap.Int("limit", req.Limit),
		zap.String("root_folder_id", req.RootFolderID),
		zap.String("strategy", req.Strategy),
		zap.Bool("dry_run", req.DryRun),
	)

	h.enqueueArtlistRun(c, req)
}

// RunSmartPipeline executes the Artlist flow with preset support
func (h *ArtlistHandler) RunSmartPipeline(c *gin.Context) {
	req, ok := apiutil.BindJSON[artlist.RunSmartRequest](c)
	if !ok {
		return
	}

	if strings.TrimSpace(req.Term) == "" {
		apiutil.BadRequest(c, "term is required")
		return
	}

	// Convert to RunTagRequest using preset
	runReq := req.ToRunTagRequest(h.presetsConfig)

	// Normalize request
	normalized := artlist.NormalizeRunTagRequest(*runReq, artlist.RunDefaults{
		DefaultRootFolderID: h.cfg.Harvester.DriveFolderID,
		MaxLimit:            500,
	})
	runReq = &normalized

	h.log.Info("artlist smart run requested",
		zap.String("term", req.Term),
		zap.String("preset", req.Preset),
		zap.Int("limit", runReq.Limit),
	)

	h.enqueueArtlistRun(c, *runReq)
}

// enqueueArtlistRun is the single enqueue path for all Artlist runs
func (h *ArtlistHandler) enqueueArtlistRun(c *gin.Context, req artlist.RunTagRequest) {
	if h.jobsService == nil {
		apiutil.InternalError(c, fmt.Errorf("jobs service not configured"))
		return
	}

	// Use common jobs system exclusively
	job, err := h.jobsService.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:       models.JobTypeArtlistRun,
		Payload:    req.ToMap(),
		MaxRetries: 3,
		ActiveKey:  artlist.RunDedupKey(req.Term, req.RootFolderID, req.Strategy, req.DryRun),
	})
	if err != nil {
		h.log.Error("failed to enqueue artlist job", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("failed to enqueue job: %w", err))
		return
	}
	apiutil.Accepted(c, artlist.JobToRunTagResponse(job))
}

// RunStatus returns the tracked status for a background artlist run
func (h *ArtlistHandler) RunStatus(c *gin.Context) {
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		apiutil.BadRequest(c, "run_id is required")
		return
	}

	resp, err := h.service.GetRunTag(c.Request.Context(), runID)
	if err != nil {
		apiutil.NotFound(c, err.Error())
		return
	}

	apiutil.OK(c, resp)
}

// Stats returns statistics about Artlist clips and search terms
func (h *ArtlistHandler) Stats(c *gin.Context) {
	stats, err := h.service.GetStats(c.Request.Context())
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to get stats: %v", err))
		return
	}

	apiutil.OK(c, stats)
}

// Search searches for Artlist clips in the database
func (h *ArtlistHandler) Search(c *gin.Context) {
	req, ok := apiutil.BindJSON[artlist.SearchRequest](c)
	if !ok {
		return
	}

	if strings.TrimSpace(req.Term) == "" {
		apiutil.BadRequest(c, "term is required")
		return
	}

	resp, err := h.service.Search(c.Request.Context(), &req)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("search failed: %v", err))
		return
	}

	apiutil.OK(c, resp)
}

// Diagnostics returns Artlist system diagnostics
func (h *ArtlistHandler) Diagnostics(c *gin.Context) {
	term := strings.TrimSpace(c.Query("term"))
	if term == "" {
		term = "test"
	}

	resp, err := h.service.Diagnostics(c.Request.Context(), term)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("diagnostics failed: %v", err))
		return
	}

	apiutil.OK(c, resp)
}

// SearchLive performs a live search using the Node.js scraper
func (h *ArtlistHandler) SearchLive(c *gin.Context) {
	term := strings.TrimSpace(c.Query("term"))
	limitStr := c.DefaultQuery("limit", "20")
	limit := 8
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
		limit = l
	}
	if limit > 50 {
		limit = 50
	}

	if term == "" {
		apiutil.BadRequest(c, "term is required")
		return
	}

	clips, err := h.service.SearchLive(c.Request.Context(), term, limit)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("live search failed: %v", err))
		return
	}

	apiutil.OK(c, gin.H{"clips": clips})
}

// Recommend handles the recommendation endpoint using clipresolver
func (h *ArtlistHandler) Recommend(c *gin.Context) {
	req, ok := apiutil.BindJSON[clipresolver.RecommendRequest](c)
	if !ok {
		return
	}

	if h.clipResolver == nil {
		apiutil.InternalError(c, fmt.Errorf("clip resolver service not available"))
		return
	}

	h.log.Info("clip resolver recommend request",
		zap.String("topic", req.Topic),
		zap.String("segment_id", req.SegmentID),
		zap.Int("queries", len(req.Queries)),
		zap.Float64("min_score", req.MinScore),
	)

	resp, err := h.clipResolver.Recommend(c.Request.Context(), &req)
	if err != nil {
		h.log.Error("clip resolver recommend failed", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("recommend failed: %v", err))
		return
	}

	apiutil.OK(c, resp)
}

func (h *ArtlistHandler) SyncCatalogs(c *gin.Context) {
	if h.catalogSync == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "catalog sync service not configured")
		return
	}

	summary, err := h.catalogSync.SyncAll(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, summary)
		return
	}

	apiutil.OK(c, summary)
}
