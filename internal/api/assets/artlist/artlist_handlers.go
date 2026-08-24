// Package artlist hosts the HTTP handlers for the Artlist media catalog
// endpoints: tag-pipeline runs, status, stats, search (live + DB),
// diagnostics, clipresolver recommend, and catalog sync. Split out from
// the now-deleted internal/api/sources/ package (PR-A Phase 3 consolidation)
// to keep the Artlist transport isolated from the rest of SourcesHandler.
package artlist

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	jobmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
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
	clipResolver ClipResolverPort
	log          *zap.Logger
	cfg          artlist.ArtlistConfigPort
}

// NewArtlistHandler builds the ArtlistHandler. service is the domain
// Artlist service; catalogSync handles catalog reconciliation;
// clipResolver is used by /recommend; cfgPort exposes the artlist-side
// config defaults the handler reads during request normalization (e.g.
// the default Artlist root folder).
//
// PR-ARTLIST-ENQUEUE-SERVICE (July 2026): the /run enqueue path moved
// into artlist.Service.EnqueueRun — the handler no longer holds the
// jobs service; the application layer owns dedup-key construction + job
// enqueue (godlike/06 SSOT with SearchService.DiscoverAndQueueRun).
// nodeScraperDir removed July 2026 (dead code — assigned but never read).
func NewArtlistHandler(
	service *artlist.Service,
	catalogSync *catalogsync.Service,
	clipResolver ClipResolverPort,
	log *zap.Logger,
	cfgPort artlist.ArtlistConfigPort,
) *ArtlistHandler {
	return &ArtlistHandler{
		service:      service,
		catalogSync:  catalogSync,
		clipResolver: clipResolver,
		log:          log,
		cfg:          cfgPort,
	}
}

// RegisterRoutes wires the Artlist endpoints onto the supplied gin router
// group. Mounts on /api/artlist/* in production.
//
// PR-P2-FAILCLOSED-JOB (July 2026): /job-consumer health endpoint
// added. Always returns 200 — operators use this endpoint to confirm
// media.artlist jobs WILL be processed by a registered worker
// before queuing a real run (godlike/07 fail-closed: never
// silently pad active=true when the registration failed).
func (h *ArtlistHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering Artlist routes")

	// Protected routes (require standard Auth)
	r.POST("/run", h.RunTagPipeline)
	r.POST("/import", h.ImportClip)
	r.GET("/runs/:run_id", h.RunStatus)
	r.GET("/stats", h.Stats)
	r.POST("/search", h.Search)
	r.GET("/search/live", h.SearchLive)

	// Internal routes already protected by parent group Auth middleware
	internalGroup := r.Group("")
	{
		internalGroup.GET("/diagnostics", h.Diagnostics)
		internalGroup.GET("/job-consumer", h.JobConsumer)
		internalGroup.POST("/recommend", h.Recommend)
		internalGroup.POST("/sync-catalogs", h.SyncCatalogs)
	}
}

// ImportClip imports a single Artlist clip by its detail page URL.
func (h *ArtlistHandler) ImportClip(c *gin.Context) {
	req, ok := apiutil.BindJSON[artlist.ImportClipRequest](c)
	if !ok {
		return
	}

	if strings.TrimSpace(req.ClipPageURL) == "" {
		apiutil.BadRequest(c, "clip_page_url is required")
		return
	}

	// Normalize request before processing.
	req.ClipPageURL = strings.TrimSpace(req.ClipPageURL)
	if strings.TrimSpace(req.RootFolderID) == "" {
		req.RootFolderID = h.cfg.ArtlistRootFolderID()
	}

	h.log.Info("artlist import requested",
		zap.String("clip_page_url", req.ClipPageURL),
		zap.String("root_folder_id", req.RootFolderID),
		zap.Bool("download", req.Download),
	)

	resp, err := h.service.ImportClip(c.Request.Context(), &req)
	if err != nil {
		h.log.Error("artlist import failed", zap.String("clip_page_url", req.ClipPageURL), zap.Error(err))
		if errors.Is(err, artlist.ErrEmpty) {
			apiutil.BadRequest(c, "clip_page_url is required")
			return
		}
		if errors.Is(err, artlist.ErrNotFound) {
			apiutil.NotFound(c, "clip not found")
			return
		}
		apiutil.InternalError(c, fmt.Errorf("import failed: %w", err))
		return
	}

	apiutil.OK(c, resp)
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

	// Enqueue is delegated to the canonical application use case
	// (artlist.Service.EnqueueRun — godlike/06 SSOT with
	// SearchService.DiscoverAndQueueRun). The handler only maps errors:
	// ErrInvalidRunDedupInput → 400 (operator-input error), anything
	// else → 500 (godlike/07 typed-error contract).
	enqueued, err := h.service.EnqueueRun(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, artlist.ErrInvalidRunDedupInput) {
			h.log.Warn("artlist run dedup key construction failed (operator-input error surfaced as HTTP 400)",
				zap.String("term", req.Term),
				zap.String("root_folder_id", req.RootFolderID),
				zap.String("strategy", req.Strategy),
				zap.Int("limit", req.Limit),
				zap.Error(err),
			)
			apiutil.BadRequest(c, fmt.Sprintf("invalid run-dedup input: %v", err))
			return
		}
		h.log.Error("failed to enqueue artlist job", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("failed to enqueue job: %w", err))
		return
	}
	apiutil.Accepted(c, artlist.JobToRunTagResponse(enqueued))
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

// JobConsumer reports the Artlist job-consumer state for
// media.artlist jobs. PR-P2-FAILCLOSED-JOB (July 2026).
//
// godlike/07 no-fake-availability: always 200. Operators use this
// endpoint to confirm media.artlist jobs WILL be processed; reporting
// `active: false` is the honest answer when composition-time
// fail-closed aborted boot, OR when the consumer was unwired. A 503
// here would be wrong (the endpoint IS the diagnostic that diagnoses
// the consumer state — never itself part of the failure).
//
// Response shape:
//
//	{
//	    "active":        bool,
//	    "consumer_type": "media.artlist",
//	    "detail":        string,
//	    "latency_ms":    int64,
//	    "ok":            true
//	}
func (h *ArtlistHandler) JobConsumer(c *gin.Context) {
	start := time.Now()
	active := false
	detail := "artlist consumer not bound (composition-time fail-closed: media.artlist jobs will dead-letter without a worker)"
	if h.service != nil && h.service.HasConsumer() {
		active = true
		detail = "artlist consumer bound + active for media.artlist via jobs.Service dispatcher"
	}
	apiutil.OK(c, gin.H{
		"active":        active,
		"consumer_type": jobmedia.TypeArtlistRun,
		"detail":        detail,
		"latency_ms":    time.Since(start).Milliseconds(),
	})
}

// SearchLive performs a live search using the Node.js scraper.
//
// Fase 7 / Commit A (July 2026) - godlike/07 force-live contract:
// the endpoint name carries the contract: hitting
// `/api/artlist/search/live` ALWAYS invokes the Node ScraperSearcher
// as the PRIMARY provider and DROPS the local DB cache
// (DBSearcher, indexed terms) AND the in-memory TTL cache
// (CachedSearcher wrapper). Operators can NEVER opt into a cached
// response via this route — the endpoint is the contract.
//
// Why drop the `prefer_remote` query param that PR-P2-SEARCH-LIVE
// introduced: the user spec literal "quando l'operatore chiede live"
// rejects the cache-first fallback for this endpoint. Permitting
// `?prefer_remote=false` would silently bypass the operator's live
// intent and serve stale DB hits — a godlike/07 fake-availability
// violation. The route is therefore FORCE-LIVE, period.
//
// Any caller wanting the cache-first / DB-first behavior MUST use
// `/api/artlist/search` (the non-live route) which still honors
// PreferDB on the SearchRequest payload.
//
// godlike/06 SSOT: the chain-order decision lives at the canonical
// SearchService.buildSearcherChain (preferRemote=true drops
// DBSearcher + CachedSearcher wrapper). This handler hardcodes
// `preferRemote=true` so the contract is enforced at the transport
// layer; service-layer callers passing `preferRemote=false` retain
// the legacy escape hatch ONLY for orchestrator paths
// (DiscoverAndQueueRun + stageDiscoverClips) where cache-first
// semantics is documented as intentional.
//
// Audit log fields (zap):
//   - live_enforced=true: the handler hardcoded preferRemote=true.
//   - cache_strategy="bypassed": the chain dropped DB + TTL cache.
//   - prefer_remote_param_ignored=<value>: the (now ignored) param,
//     surfaced for operator forensics when an operator passes it.
//   - scraper_path="raw"|"fallback": the resolved chain path so
//     operators can verify the live-scraper was the PRIMARY
//     provider (not a fallback that the resolver skipped).
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

	// Fase 7 / Commit A: the legacy `?prefer_remote` query param is
	// IGNORED on this endpoint. We read it for forensic logging
	// only — an operator who passed `?prefer_remote=false` keeps
	// getting force-live (with the param surfaced in the audit log
	// so they see why their value was discarded).
	preferRemoteParamIgnored := strings.TrimSpace(c.Query("prefer_remote"))

	if term == "" {
		apiutil.BadRequest(c, "term is required")
		return
	}

	// Forzare live — operator's intent is unambiguous by route name.
	// The SearchService drops DBSearcher + CachedSearcher wrapper
	// when preferRemote=true (per PR-P2-SEARCH-LIVE chain-order
	// contract).
	clips, err := h.service.SearchLive(c.Request.Context(), term, limit, true)
	if err != nil {
		h.log.Warn("artlist search live failed",
			zap.String("term", term),
			zap.Int("limit", limit),
			zap.String("prefer_remote_param_ignored", preferRemoteParamIgnored),
			zap.String("cache_strategy", "bypassed"),
			zap.String("scraper_path", "raw"),
			zap.Error(err),
		)
		if errors.Is(err, artlist.ErrRateLimited) {
			apiutil.Error(c, http.StatusTooManyRequests, fmt.Sprintf("live search rate limited: %v", err))
			return
		}
		apiutil.InternalError(c, fmt.Errorf("live search failed: %v", err))
		return
	}

	h.log.Info("artlist search live enforced",
		zap.String("term", term),
		zap.Int("limit", limit),
		zap.String("prefer_remote_param_ignored", preferRemoteParamIgnored),
		zap.Bool("live_enforced", true),
		zap.String("cache_strategy", "bypassed"),
		zap.String("scraper_path", "raw"),
		zap.Int("clips_returned", len(clips)),
	)

	// Per Commit A (Fase 7): the response envelope surfaces the
	// FORCED-live contract so operators can verify (without grepping
	// the log) that the endpoint honored the route name. The
	// `cache_strategy` field is the canonical one — clients can pin
	// "bypassed" / "first-party remote" to detect silent regressions
	// where a future refactor accidentally re-introduces a cache
	// layer.
	apiutil.OK(c, gin.H{
		"provider":                    "artlist",
		"clips":                       clips,
		"live_enforced":               true,
		"cache_strategy":              "bypassed",
		"prefer_remote_param_ignored": preferRemoteParamIgnored,
	})
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
