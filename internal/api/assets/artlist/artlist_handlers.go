// Package artlist hosts the HTTP handlers for the Artlist media catalog
// endpoints: tag-pipeline runs, status, stats, search (live + DB),
// diagnostics, clipresolver recommend, and catalog sync. Split out from
// the legacy flat internal/api/sources/ package as part of PR-A Phase 3
// to keep the Artlist transport isolated from the rest of SourcesHandler.
package artlist

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ClipResolverPort is a local interface replacing the removed
// clipresolver.Service (package internal/application/assets/clipresolver).
type ClipResolverPort interface {
	Recommend(ctx context.Context, req *ClipResolverRecommendRequest) (*ClipResolverRecommendResponse, error)
}

// ClipResolverRecommendRequest is a local type replacing clipresolver.RecommendRequest.
type ClipResolverRecommendRequest struct {
	Topic     string   `json:"topic"`
	SegmentID string   `json:"segment_id"`
	Queries   []string `json:"queries"`
	MinScore  float64  `json:"min_score"`
}

// ClipResolverRecommendResponse is a local type for recommend responses.
type ClipResolverRecommendResponse struct {
	Results []ClipResolverRecommendResult `json:"results"`
}

// ClipResolverRecommendResult is a local type.
type ClipResolverRecommendResult struct {
	ClipID    string  `json:"clip_id"`
	Score     float64 `json:"score"`
	DriveLink string  `json:"drive_link"`
}

// ArtlistHandler owns the HTTP transport for Artlist operations:
// tag-pipeline runs (run/status/stats), search (DB + live), diagnostics,
// recommendation, and catalog sync. Construction mirrors the legacy
// api/sources package, but lives here (package artlist) so the handler
// can be tested in isolation from the rest of SourcesHandler.
type ArtlistHandler struct {
	service        *artlist.Service
	catalogSync    *catalogsync.Service
	jobsService    jobservice.Service
	clipResolver   ClipResolverPort
	nodeScraperDir string
	log            *zap.Logger
	cfg            *config.Config
}

// NewArtlistHandler builds the ArtlistHandler. service is the domain
// Artlist service; catalogSync handles catalog reconciliation; jobsSvc
// enqueues the artlist.run job; clipResolver is used by /recommend;
// nodeScraperDir is the path to the persistent Node scraper dir.
func NewArtlistHandler(
	service *artlist.Service,
	catalogSync *catalogsync.Service,
	jobsService jobservice.Service,
	clipResolver ClipResolverPort,
	nodeScraperDir string,
	log *zap.Logger,
	cfg *config.Config,
) *ArtlistHandler {
	return &ArtlistHandler{
		service:        service,
		catalogSync:    catalogSync,
		jobsService:    jobsService,
		clipResolver:   clipResolver,
		nodeScraperDir: nodeScraperDir,
		log:            log,
		cfg:            cfg,
	}
}

// RegisterRoutes wires the Artlist endpoints onto the supplied gin router
// group. Mounts on /api/artlist/* in production.
func (h *ArtlistHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering Artlist routes")

	// Protected routes (require standard Auth)
	r.POST("/run", h.RunTagPipeline)
	r.GET("/runs/:run_id", h.RunStatus)
	r.GET("/stats", h.Stats)

	// Internal routes already protected by parent group Auth middleware
	internalGroup := r.Group("")
	{
		internalGroup.GET("/diagnostics", h.Diagnostics)
		internalGroup.POST("/search", h.Search)
		internalGroup.POST("/search/live", h.SearchLive)
		internalGroup.POST("/recommend", h.Recommend)
		internalGroup.POST("/sync-catalogs", h.SyncCatalogs)
	}
}

// RunTagPipeline executes the full Artlist flow for a tag.
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
		DefaultRootFolderID: artlist.ResolveRootFolderID(h.cfg),
		MaxLimit:            500,
	})
	if strings.TrimSpace(req.RootFolderID) == "" {
		apiutil.BadRequest(c, "artlist root folder is not configured")
		return
	}

	h.log.Info("artlist run requested",
		zap.String("term", req.Term),
		zap.Int("limit", req.Limit),
		zap.String("root_folder_id", req.RootFolderID),
		zap.String("strategy", req.Strategy),
		zap.Bool("dry_run", req.DryRun),
	)

	h.enqueueArtlistRun(c, req)
}

// enqueueArtlistRun is the single enqueue path for all Artlist runs
func (h *ArtlistHandler) enqueueArtlistRun(c *gin.Context, req artlist.RunTagRequest) {
	if h.jobsService == nil {
		apiutil.InternalError(c, fmt.Errorf("jobs service not configured"))
		return
	}

	// Use common jobs system exclusively
	job, err := h.jobsService.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:       "artlist.run",
		Payload:    (&artlist.JobCodec{}).PayloadFromRequest(&req),
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
	req, ok := apiutil.BindJSON[ClipResolverRecommendRequest](c)
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

// SyncCatalogs synchronizes all artlist catalogs.
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
