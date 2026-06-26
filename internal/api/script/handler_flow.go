// Package script (api/script) — ScriptFlowHandler is the canonical entry
// point for the script-flow HTTP surface.
//
// Concrete adapter packages from `internal/infrastructure/**` are not
// imported here; ClipServices is pre-built by wire_script.go and the
// handler consumes it through narrow local interfaces (DriveFolderClient,
// DocumentCreator). Admin token is held as a plain string and exposed
// via the local AdminTokenProvider interface so RequireAdminToken can
// accept the handler directly without intermediate adapters.

package script

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Local interfaces ────────────────────────────────────────────────────────

// DriveFolderClient abstracts folder creation for Drive resolution.
type DriveFolderClient interface {
	GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)
}

// DocumentCreator abstracts Google Doc creation.
type DocumentCreator interface {
	CreateDoc(ctx context.Context, title, content, folderID string) (docURL, docID string)
}

// ── Handler ─────────────────────────────────────────────────────────────────

type ScriptFlowHandler struct {
	engine     *scripts.Engine
	imgService *images.Service
	// Wave 16 (June 2026): typed ports — `realtimeSvc` is
	// `scripts.RealtimeSearchService`, `associationSvc` is
	// `scripts.AssocSearchService`. Packages removed in commit
	// d61068b3 → fields stay typed-nil. NewScriptFlowHandler assigns
	// directly from deps.Realtime / deps.Association (typed-to-typed).
	realtimeSvc       scripts.RealtimeSearchService
	associationSvc    scripts.AssocSearchService
	voService         *voiceover.Service
	assetTreeSvc      *assettree.Service
	groupsResolver    *voiceover.GroupsResolver
	clipSourceBuilder *scripts.ClipSourceBuilder
	mediaCurator      *scripts.MediaCurator
	sectionRegen      *scripts.SectionRegenerator
	cacheEviction     *scripts.CacheEvictionUseCase
	genBatchUC        *scripts.GenerateBatchUseCase
	insightBuilder    *ScriptInsightBuilder
	clipServices      scripts.ClipServices
	driveFolderClient DriveFolderClient
	documentCreator   DocumentCreator
	jobsSvc           jobservice.Service
	scriptsRepo       scripts.ScriptRepository
	memorySvc         *gemmamemory.Service
	driveFolderID     string
	adminToken        string
	log               *zap.Logger
	genSvc            GenerationService // backing service for /generate-from-clips, /generate-with-images
	gates             FeatureGates      // per-route feature flags (clips / images / docs)

	pipelineUC *scripts.PipelineUseCase
}

// ScriptFlowDeps groups all constructor inputs.
type ScriptFlowDeps struct {
	Engine          *scripts.Engine
	Section         *scripts.SectionRegenerator
	CacheEviction   *scripts.CacheEvictionUseCase
	GenBatchUC      *scripts.GenerateBatchUseCase
	PipelineUseCase *scripts.PipelineUseCase

	Image *images.Service
	// Wave 16 (June 2026): typed ports — replace the `interface{}`
	// carrier for the script-side realtime + association consumers
	// (packages removed in commit d61068b3; fields stay typed-nil).
	// Compile-time enforcement replaces the prior runtime safety net.
	Realtime    scripts.RealtimeSearchService
	Association scripts.AssocSearchService
	Voiceover   *voiceover.Service
	AssetTree   *assettree.Service

	ClipSourceBuilder *scripts.ClipSourceBuilder
	MediaCurator      *scripts.MediaCurator

	ScriptsRepo scripts.ScriptRepository
	Memory      *gemmamemory.Service
	Jobs        jobservice.Service

	AdminToken            string
	DriveFolderClient     DriveFolderClient
	DocumentCreator       DocumentCreator
	DriveScriptsGenFolder string
	ClipServices          scripts.ClipServices // pre-built in wire_script.go
	Log                   *zap.Logger

	GenService GenerationService // backs /generate-from-clips, /generate-with-images
	Gates      FeatureGates      // per-route feature flags (clips / images / docs)
}

func NewScriptFlowHandler(deps ScriptFlowDeps) *ScriptFlowHandler {
	log := deps.Log
	if log == nil {
		log = zap.NewNop()
	}
	clipSvc := deps.ClipServices

	var groupsResolver *voiceover.GroupsResolver
	if deps.AssetTree != nil {
		if gr, err := voiceover.NewGroupsResolver(deps.AssetTree, log); err != nil {
			log.Warn("ScriptFlowHandler groups_resolver not initialized", zap.Error(err))
		} else {
			groupsResolver = gr
		}
	}

	h := &ScriptFlowHandler{
		engine:            deps.Engine,
		imgService:        deps.Image,
		associationSvc:    deps.Association,
		voService:         deps.Voiceover,
		assetTreeSvc:      deps.AssetTree,
		groupsResolver:    groupsResolver,
		clipSourceBuilder: deps.ClipSourceBuilder,
		mediaCurator:      deps.MediaCurator,
		sectionRegen:      deps.Section,
		cacheEviction:     deps.CacheEviction,
		genBatchUC:        deps.GenBatchUC,
		driveFolderClient: deps.DriveFolderClient,
		documentCreator:   deps.DocumentCreator,
		jobsSvc:           deps.Jobs,
		scriptsRepo:       deps.ScriptsRepo,
		memorySvc:         deps.Memory,
		driveFolderID:     deps.DriveScriptsGenFolder,
		adminToken:        deps.AdminToken,
		log:               log,
		clipServices:      clipSvc,
		insightBuilder:    NewScriptInsightBuilder(log, 12, clipSvc),
		pipelineUC:        deps.PipelineUseCase,
		genSvc:            deps.GenService,
		gates:             deps.Gates,
	}

	return h
}

// ── Local AdminTokenProvider port ──────────────────────────────────────────
//
// Two-method interface consumed by RequireAdminToken. The canonical
// concrete is pkg/middleware.TokenSecurityAdapter; ScriptFlowHandler
// itself satisfies the port structurally so it can be passed in without
// an intermediate adapter struct.
type AdminTokenProvider interface {
	EnableAuth() bool
	AdminToken() string
}

// ── Route registration ──────────────────────────────────────────────────────

func (h *ScriptFlowHandler) registerJobRoutes(r *gin.RouterGroup) {
	jobs := r.Group("")
	jobs.Use(RequireAdminToken(h))
	jobs.GET("/jobs/:job_id", h.GetJobStatus)
	jobs.GET("/jobs/:job_id/full", h.GetJobFullStatus)
}

// RequireAdminToken wraps middleware.RequireAdminToken accepting the local
// AdminTokenProvider interface instead of the dense configuration struct.
func RequireAdminToken(cfg AdminTokenProvider) gin.HandlerFunc {
	// Delegate to the canonical middleware via an adapter that bridges
	// AdminTokenProvider → adapter fields.
	return func(c *gin.Context) {
		if cfg == nil || !cfg.EnableAuth() {
			c.Set("is_admin", true)
			c.Next()
			return
		}
		expected := strings.TrimSpace(cfg.AdminToken())
		if expected == "" {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "RequireAdminToken misconfigured (VELOX_ADMIN_TOKEN is empty)",
			})
			c.Abort()
			return
		}
		// Read token from X-Velox-Admin-Token or Authorization: Bearer header
		provided := extractHeaderToken(c)
		if provided == expected {
			c.Set("is_admin", true)
			c.Next()
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "admin token required",
		})
		c.Abort()
	}
}

func extractHeaderToken(c *gin.Context) string {
	tok := strings.TrimSpace(c.GetHeader("X-Velox-Admin-Token"))
	if tok != "" {
		return tok
	}
	bearer := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	return strings.TrimSpace(bearer)
}

// RegisterRoutes mounts every script-flow route under r. Each legacy
// generation route (/generate-from-clips, /generate-with-images, the
// /generate-batch pair) is feature-gated; the flow routes (/curate,
// /generate-from-catalog, /:id/sections/:section_id/regenerate,
// /cache/evict, /jobs/:job_id[/full]) are always mounted because they
// cover all script-flow use cases regardless of which generation path
// is enabled.
func (h *ScriptFlowHandler) RegisterRoutes(r *gin.RouterGroup) {
	if h.gates.ScriptClipsEnabled {
		r.POST("/generate-from-clips", h.GenerateFromClips)
	}
	if h.gates.ScriptImagesEnabled {
		r.POST("/generate-with-images", h.GenerateWithImages)
	}
	if h.gates.ScriptDocsEnabled {
		r.POST("/generate-batch", h.GenerateBatch)
		r.GET("/generate-batch/progress", h.GetBatchProgress)
	}

	r.POST("/generate-from-catalog", h.GenerateFromCatalog)
	r.POST("/curate", h.Curate)
	h.registerJobRoutes(r)
	r.POST("/:id/sections/:section_id/regenerate", h.RegenerateSection)
	r.POST("/cache/evict", h.EvictCache)
}

// ── Accessors for job services ──────────────────────────────────────────────

// EnableAuth + AdminToken implement the local AdminTokenProvider
// interface so handlers can be passed directly to RequireAdminToken
// without a wrapping adapter. EnableAuth resolves true when an admin
// token has been wired (adminToken field is non-empty); AdminToken
// returns the configured token verbatim.
func (h *ScriptFlowHandler) EnableAuth() bool { return h.adminToken != "" }

func (h *ScriptFlowHandler) AdminToken() string {
	if h == nil {
		return ""
	}
	return h.adminToken
}

func (h *ScriptFlowHandler) GetVoiceoverService() *voiceover.Service {
	return h.voService
}

func (h *ScriptFlowHandler) GetGroupsResolver() *voiceover.GroupsResolver {
	return h.groupsResolver
}

func (h *ScriptFlowHandler) ResolveDriveFolderID(ctx context.Context, input, defaultRootID string) (string, error) {
	return h.resolveDriveFolderID(ctx, input, defaultRootID)
}

func (h *ScriptFlowHandler) MaybeCreateGoogleDoc(ctx context.Context, title, content, folderID string, createDoc bool) (string, string) {
	if !createDoc {
		return "", ""
	}
	return h.documentCreator.CreateDoc(ctx, title, content, folderID)
}

// ── Job endpoints ───────────────────────────────────────────────────────────

// ── Legacy generation endpoints (/generate-from-clips, /generate-with-images) ─

// GenerateFromClips handles POST /generate-from-clips.
func (h *ScriptFlowHandler) GenerateFromClips(c *gin.Context) {
	if h.genSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "generation service not initialized"})
		return
	}
	var spec scriptpkg.GenerationSpec
	if err := c.ShouldBindJSON(&spec); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload"})
		return
	}
	result, err := h.genSvc.EnqueueFromClips(c.Request.Context(), spec)
	if err != nil {
		status := mapErrorToHTTP(err)
		c.JSON(status, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "job_id": result.JobID, "status": result.JobStatus})
}

// GenerateWithImages handles POST /generate-with-images.
func (h *ScriptFlowHandler) GenerateWithImages(c *gin.Context) {
	if h.genSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "generation service not initialized"})
		return
	}
	var spec scriptpkg.GenerationSpec
	if err := c.ShouldBindJSON(&spec); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload"})
		return
	}
	result, err := h.genSvc.EnqueueWithImages(c.Request.Context(), spec)
	if err != nil {
		status := mapErrorToHTTP(err)
		c.JSON(status, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "job_id": result.JobID, "status": result.JobStatus})
}

func (h *ScriptFlowHandler) GetJobFullStatus(c *gin.Context) {
	if h.jobsSvc == nil {
		api.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}
	jobID := strings.TrimSpace(c.Param("job_id"))
	if jobID == "" {
		api.BadRequest(c, "job_id is required")
		return
	}
	job, err := h.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		api.NotFound(c, fmt.Sprintf("job not found: %v", err))
		return
	}
	events, err := h.jobsSvc.ListEvents(c.Request.Context(), jobID)
	if err != nil {
		h.log.Warn("failed to list job events", zap.String("job_id", jobID), zap.Error(err))
		events = nil
	}
	c.JSON(http.StatusOK, gin.H{
		"ok": true, "job_id": job.ID, "type": job.Type, "status": job.Status,
		"priority": job.Priority, "progress": job.Progress, "error": job.Error,
		"result": job.Result, "retry_count": job.RetryCount, "max_retries": job.MaxRetries,
		"correlation_id": job.CorrelationID, "created_at": job.CreatedAt,
		"started_at": job.StartedAt, "completed_at": job.CompletedAt,
		"updated_at": job.UpdatedAt, "events": events,
	})
}

// GenerateBatch handles POST /generate-batch.
//
// PR-A (June 2026): thin transport — the canonical orchestrator now
// lives in scripts.GenerateBatchUseCase (Run takes a typed input and
// returns a typed output: Async or Response, plus DocTitle). The
// handler is responsible only for:
//   - parsing the JSON body
//   - extracting the Idempotency-Key header (transport-only concern)
//   - calling the use case
//   - translating typed domain errors into HTTP status codes
//   - serialising the typed output (Async vs Response) to JSON
//
// Adding logic here is a code smell — extend
// scripts.GenerateBatchUseCase instead.
func (h *ScriptFlowHandler) GenerateBatch(c *gin.Context) {
	var req scripts.GenerateBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload"})
		return
	}

	if h.genBatchUC == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "batch use case not initialized"})
		return
	}

	idemKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))

	out, err := h.genBatchUC.Run(c.Request.Context(), scripts.GenerateBatchInput{
		Request:        &req,
		IdempotencyKey: idemKey,
	})
	if err != nil {
		h.mapBatchError(c, err)
		return
	}
	if out == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "use case returned nil output"})
		return
	}

	if out.Async != nil {
		c.JSON(http.StatusOK, gin.H{
			"ok":         true,
			"async":      true,
			"job_id":     out.Async.JobID,
			"status":     out.Async.Status,
			"status_url": out.Async.StatusURL,
			"doc_title":  out.DocTitle,
		})
		return
	}

	resp := out.Response
	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"async":     false,
		"doc_title": resp.DocTitle,
		"doc_id":    resp.DocID,
		"doc_link":  resp.DocLink,
		"scripts":   resp.Scripts,
	})
}

// mapBatchError translates a use-case error into an HTTP response.
// Domain typed errors map to specific codes; everything else falls
// through to a 500 Internal Server Error. Mirrors the regen/error
// mappers in handler_flow_ops.go.
func (h *ScriptFlowHandler) mapBatchError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, scripts.ErrGenerateBatchInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
	case errors.Is(err, scripts.ErrGenerateBatchMissing):
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": err.Error()})
	case errors.Is(err, scripts.ErrGenerateBatchAsyncFailed), errors.Is(err, scripts.ErrGenerateBatchSyncFailed):
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
	default:
		if h.log != nil {
			h.log.Error("generate-batch use case failed", zap.Error(err))
		}
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
	}
}

// GetBatchProgress handles GET /generate-batch/progress.
func (h *ScriptFlowHandler) GetBatchProgress(c *gin.Context) {
	if h.jobsSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "jobs service not initialized"})
		return
	}
	jobID := strings.TrimSpace(c.Query("job_id"))
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "job_id is required"})
		return
	}
	job, err := h.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": fmt.Sprintf("job not found: %v", err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"job_id":   job.ID,
		"status":   job.Status,
		"progress": job.Progress,
		"error":    job.Error,
	})
}

func (h *ScriptFlowHandler) GetJobStatus(c *gin.Context) {
	if h.jobsSvc == nil {
		api.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}
	jobID := strings.TrimSpace(c.Param("job_id"))
	if jobID == "" {
		api.BadRequest(c, "job_id is required")
		return
	}
	job, err := h.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		api.NotFound(c, fmt.Sprintf("job not found: %v", err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok": true, "job_id": job.ID, "status": job.Status,
		"progress": job.Progress, "error": job.Error, "result": job.Result,
	})
}
