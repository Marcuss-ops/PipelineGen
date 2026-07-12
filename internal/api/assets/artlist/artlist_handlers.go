// Package artlist hosts the HTTP handlers for the Artlist media catalog
// endpoints: tag-pipeline runs, status, stats, search (live + DB),
// diagnostics, clipresolver recommend, and catalog sync. Split out from
// the now-deleted internal/api/sources/ package (PR-A Phase 3 consolidation)
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
//
// The `cfg` field is the 1-method typed port `artlist.ArtlistConfigPort`,
// not the concrete `*config.Config`. The composition root wraps the
// config concrete in `internal/app/artlist_config_adapter.go` so this
// package stays free of infrastructure-layer imports
// (AGENTS.md Pattern 0).
type ArtlistHandler struct {
	service      *artlist.Service
	catalogSync  *catalogsync.Service
	jobsService  jobservice.Service
	clipResolver ClipResolverPort
	log          *zap.Logger
	cfg          artlist.ArtlistConfigPort
}

// NewArtlistHandler builds the ArtlistHandler. service is the domain
// Artlist service; catalogSync handles catalog reconciliation; jobsSvc
// enqueues the artlist.run job; clipResolver is used by /recommend;
// cfgPort exposes the artlist-side config defaults the handler reads
// during request normalization (e.g. the default Artlist root folder).
// nodeScraperDir removed July 2026 (dead code — assigned but never read).
func NewArtlistHandler(
	service *artlist.Service,
	catalogSync *catalogsync.Service,
	jobsService jobservice.Service,
	clipResolver ClipResolverPort,
	log *zap.Logger,
	cfgPort artlist.ArtlistConfigPort,
) *ArtlistHandler {
	return &ArtlistHandler{
		service:      service,
		catalogSync:  catalogSync,
		jobsService:  jobsService,
		clipResolver: clipResolver,
		log:          log,
		cfg:          cfgPort,
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
	r.POST("/search", h.Search)
	r.GET("/search/live", h.SearchLive)

	// Internal routes already protected by parent group Auth middleware
	internalGroup := r.Group("")
	{
		internalGroup.GET("/diagnostics", h.Diagnostics)
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

	// Normalize request before enqueue. `h.cfg` is a typed port
	// (`artlist.ArtlistConfigPort`); `ArtlistRootFolderID()` exposes
	// only the default Artlist root folder the handler reads during
	// request normalization. The port is the narrow contraction of
	// `artlist.ResolveRootFolderID(cfg)` so the api package stays free
	// of infrastructure-layer imports.
	req = artlist.NormalizeRunTagRequest(req, artlist.RunDefaults{
		DefaultRootFolderID: h.cfg.ArtlistRootFolderID(),
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
		Type:       "media.artlist",
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

// SearchLive performs a live search using the Node.js scraper.
//
// PR-P2-SEARCH-LIVE (July 2026): the handler reads an optional
// `?prefer_remote=true|false` query parameter. **Default for this
// endpoint is `true`** per user-spec contract: when the param is
// omitted, the Node ScraperSearcher is invoked as the PRIMARY
// provider and the local DB cache (DBSearcher, indexed terms) AND
// the in-memory TTL cache (CachedSearcher wrapper) are BOTH
// COMPLETELY DROPPED from the chain. Operators wanting the legacy
// cache-first behavior can opt back in with `?prefer_remote=false`.
//
// godlike/06 SSOT: the chain-order decision lives at the canonical
// SearchService.buildSearcherChain — this handler only translates
// the query parameter into the boolean flag.
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

	// PR-P2-SEARCH-LIVE: prefer_remote query parsing.
	// DefaultQuery("prefer_remote", "true") → user-spec literal: default
	// true for /api/artlist/search/live. ParseBool accepts "1", "t",
	// "true", "T", "TRUE" (and "0", "f", "false", "F", "FALSE"). On
	// unparseable input we fall back to false (legacy cache-first
	// semantics) rather than panic — explicit `true|false` is what the
	// client requested.
	preferRemoteStr := c.DefaultQuery("prefer_remote", "true")
	preferRemote, _ := strconv.ParseBool(preferRemoteStr)

	if term == "" {
		apiutil.BadRequest(c, "term is required")
		return
	}

	h.log.Info("artlist search live requested",
		zap.String("term", term),
		zap.Int("limit", limit),
		zap.Bool("prefer_remote", preferRemote),
		zap.String("prefer_remote_raw", preferRemoteStr),
	)

	clips, err := h.service.SearchLive(c.Request.Context(), term, limit, preferRemote)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("live search failed: %v", err))
		return
	}

	// PR-P2-SEARCH-LIVE: surface `prefer_remote` in the response
	// envelope so operators see which mode the handler actually used
	// (parse failures fall back to false; explicit `true|false` is
	// preserved verbatim). Operators can verify the chain mode
	// without inspecting the log.
	apiutil.OK(c, gin.H{"clips": clips, "prefer_remote": preferRemote})
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
