// Package script (api/script) — ScriptFlowHandler is the canonical entry
// point for the script-flow HTTP surface.
//
// Concrete adapter packages from `internal/infrastructure/**` are not
// imported here; ClipServices is pre-built by wire_script.go and the
// handler consumes it through narrow local interfaces (DriveFolderClient,
// DocumentCreator). Admin token is held as a plain string and exposed
// via the local AdminTokenProvider interface so RequireAdminToken can
// accept the handler directly without intermediate adapters.
//
// PR7 (June 2026): removed legacy per-mode endpoints (GenerateFromClips,
// GenerateWithImages, GenerateBatch, GetBatchProgress) and their route
// registrations — superseded by POST /api/script/generate (PR6).
// GenerateFromCatalog removed — superseded by the unified endpoint.
// PipelineUseCase, GenerateBatchUseCase, GenerationService, FeatureGates
// references removed — all legacy wiring gone.
//
// PR11 (June 2026): legacy routes re-added as deprecated adapters that
// translate old request formats to GenerationEnvelopeV2 and forward to
// the canonical enqueue. Each adds X-Deprecated: true header and
// increments a global deprecation counter. See handler_legacy_adapters.go.

package script

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptdto "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
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

// JobFullStatusHandler is the narrow port for the canonical
// /api/script/jobs/:job_id/full delegator.
//
// It is satisfied structurally by *jobsapi.JobsHandler
// (which exposes `GetFull(c *gin.Context)`); the script
// module does NOT import the jobs package directly — the
// composition root (internal/app/wire_script.go) wires the
// concrete handler through ScriptFlowDeps.JobFullStatus.
//
// Issue 9 / P2 (June 2026): collapses the duplicated
// script-side job status response shape into the canonical
// /api/jobs/:id/full handler. The pre-Issue-9 script handler
// had its own GetJobFullStatus body (calls jobsSvc.Get +
// jobsSvc.ListEvents + return script-shaped response) which
// duplicated the jobs module's GetFull logic with only a
// different response wrapper. The delegator removes the
// duplication AND preserves the admin-token gate that the
// script route already has (middleware runs before the
// handler call).
type JobFullStatusHandler interface {
	GetFull(c *gin.Context)
}

// ── Handler ─────────────────────────────────────────────────────────────────

type ScriptFlowHandler struct {
	engine     *usecase.Engine
	imgService *images.Service
	// Wave 16 (June 2026): typed ports — `realtimeSvc` is
	// `usecase.RealtimeSearchService`, `associationSvc` is
	// `usecase.AssocSearchService`. Packages removed in commit
	// d61068b3 → fields stay typed-nil. NewScriptFlowHandler assigns
	// directly from deps.Realtime / deps.Association (typed-to-typed).
	realtimeSvc       usecase.RealtimeSearchService
	associationSvc    usecase.AssocSearchService
	voService         *voiceover.Service
	assetTreeSvc      *assettree.Service
	groupsResolver    *voiceover.GroupsResolver
	clipSourceBuilder *usecase.ClipSourceBuilder
	mediaCurator      *scriptdto.MediaCurator
	sectionRegen      *usecase.SectionRegenerator
	cacheEviction     *usecase.CacheEvictionUseCase
	insightBuilder    *ScriptInsightBuilder
	clipServices      usecase.ClipServices
	driveFolderClient DriveFolderClient
	documentCreator   DocumentCreator
	jobsSvc           jobservice.Service
	// Issue 9 / P2 (June 2026): narrow port for the
	// /api/script/jobs/:job_id/full delegator. The
	// script route's /full handler calls this port
	// instead of reimplementing the jobs.GetFull logic
	// with a different response wrapper. See
	// JobFullStatusHandler doc above.
	jobFullStatus     JobFullStatusHandler
	scriptsRepo       usecase.ScriptRepository
	// Issue 4 (June 2026, P1): registry is the canonical job-type
	// Registry, attached at composition time so EnqueueGenerationJob
	// can source MaxRetries from registry.DefaultMaxRetries(jType)
	// instead of the pre-Issue-4 hard-coded 3-retry fallback.
	// Optional (nil-tolerant) so legacy test fixtures that don't
	// wire the registry keep working — EnqueueGenerationJob leaves
	// MaxRetries=0 in that case and the JobsService fallback (now
	// registry-aware) becomes the safety net.
	registry *appjobs.Registry
	memorySvc         *adapters.Service
	harvestSvc        AutoHarvestService
	driveFolderID     string
	adminToken        string
	// PR-FIX (June 2026): lightweight clip-name searcher for
	// GET /script/clips/search?q= discovery endpoint.
	clipsSearcher     ClipSearcher
	log               *zap.Logger
}

type AutoHarvestService interface {
	EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (string, error)
}

// ScriptFlowDeps groups all constructor inputs.
type ScriptFlowDeps struct {
	Engine        *usecase.Engine
	Section       *usecase.SectionRegenerator
	CacheEviction *usecase.CacheEvictionUseCase

	Image *images.Service
	// Wave 16 (June 2026): typed ports — replace the `interface{}`
	// carrier for the script-side realtime + association consumers
	// (packages removed in commit d61068b3; fields stay typed-nil).
	// Compile-time enforcement replaces the prior runtime safety net.
	Realtime    usecase.RealtimeSearchService
	Association usecase.AssocSearchService
	Voiceover   *voiceover.Service
	AssetTree   *assettree.Service

	ClipSourceBuilder *usecase.ClipSourceBuilder
	MediaCurator      *scriptdto.MediaCurator
	Harvest           AutoHarvestService

	ScriptsRepo usecase.ScriptRepository
	Memory      *adapters.Service
	Jobs        jobservice.Service
	// Issue 9 / P2 (June 2026): narrow port for the
	// canonical /api/script/jobs/:job_id/full
	// delegator. The composition root wires a
	// *jobsapi.JobsHandler here so the script route
	// delegates the /full fetch to the same handler
	// the jobs module exposes at /api/jobs/:id/full.
	// Optional (nil-tolerant for test fixtures; the
	// GetJobFullStatus method keeps a no-op guard).
	JobFullStatus JobFullStatusHandler		// Issue 4 (June 2026, P1): optional canonical job-type registry
		// used by EnqueueGenerationJob to source MaxRetries from
		// registry.DefaultMaxRetries(jType). Optional — nil preserves
		// the legacy hard-coded 3-retry fallback path through the
		// JobsService. Composition root will pass appjobs.Compose().
		Registry *appjobs.Registry

		// PR-FIX (June 2026): optional clip-name searcher for
		// GET /script/clips/search?q= discovery endpoint.
		// Nil → endpoint returns 503.
		ClipsSearcher ClipSearcher

	AdminToken            string
	DriveFolderClient     DriveFolderClient
	DocumentCreator       DocumentCreator
	DriveScriptsGenFolder string
	ClipServices          usecase.ClipServices // pre-built in wire_script.go
	Log                   *zap.Logger
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
		realtimeSvc:       deps.Realtime,
		associationSvc:    deps.Association,
		voService:         deps.Voiceover,
		assetTreeSvc:      deps.AssetTree,
		groupsResolver:    groupsResolver,
		clipSourceBuilder: deps.ClipSourceBuilder,
		mediaCurator:      deps.MediaCurator,
		sectionRegen:      deps.Section,
		cacheEviction:     deps.CacheEviction,
		driveFolderClient: deps.DriveFolderClient,
		documentCreator:   deps.DocumentCreator,
		jobsSvc:           deps.Jobs,
		jobFullStatus:     deps.JobFullStatus,
		scriptsRepo:       deps.ScriptsRepo,
		memorySvc:         deps.Memory,
		harvestSvc:        deps.Harvest,
		driveFolderID:     deps.DriveScriptsGenFolder,
		adminToken:        deps.AdminToken,
		log:               log,
		clipServices:      clipSvc,
		clipsSearcher:     deps.ClipsSearcher,
		insightBuilder:    NewScriptInsightBuilder(log, 12, clipSvc),
		// Issue 4 (June 2026, P1): plumb the typed *appjobs.Registry
		// through to the enqueue helpers so MaxRetries is sourced from
		// registry.DefaultMaxRetries(script.generate)=2 instead of the
		// pre-Issue-4 hard-coded 3-retry fallback.
		registry: deps.Registry,
	}

	return h
}

// ── Local AdminTokenProvider port ──────────────────────────────────────────
//
// Two-method interface consumed by RequireAdminToken. The canonical
// concrete is internal/api/middleware.TokenSecurityAdapter;
// ScriptFlowHandler itself satisfies the port structurally so it can
// be passed in without an intermediate adapter struct.
type AdminTokenProvider interface {
	EnableAuth() bool
	AdminToken() string
}

// ── Route registration ──────────────────────────────────────────────────────

func (h *ScriptFlowHandler) registerJobRoutes(r *gin.RouterGroup) {
	// Issue 9 / P2 followup (June 2026): the /full route
	// DELEGATES to JobsHandler.GetFull (which reads
	// `c.Param("id")`). To keep the delegator a zero-rewrite
	// forward, the route param here MUST be named "id" — the
	// same name the Jobs module uses at /api/jobs/:id/full.
	// Pre-Issue-9 the param was "job_id" which broke the
	// delegator (empty "id" → JobsHandler.GetFull 404'd).
	// The non-/full route is also aligned to "id" for the
	// same reason (consistency + future-proofing if the
	// non-/full route is also collapsed in a follow-up).
	jobs := r.Group("")
	jobs.Use(RequireAdminToken(h))
	jobs.GET("/jobs/:id", h.GetJobStatus)
	jobs.GET("/jobs/:id/full", h.GetJobFullStatus)
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

// RegisterRoutes mounts every script-flow route under r. The unified
// generation endpoint (POST /generate) handles all generation modes.
// Legacy routes (generate-from-clips, generate-with-images, generate-batch,
// curate) are registered as deprecated adapters that translate old request
// formats to GenerationEnvelopeV2 and forward to the canonical enqueue.
// Flow routes (regenerate, evict, job status) are always mounted.
func (h *ScriptFlowHandler) RegisterRoutes(r *gin.RouterGroup) {
	// Unified generation endpoint (replaces all legacy per-mode endpoints).
	r.POST("/generate", h.Generate)

	// Legacy routes — deprecated adapters (PR 11, June 2026).
	// Each translates the old request shape to GenerationEnvelopeV2,
	// enqueues as script.generate, and adds X-Deprecated: true header.
	r.POST("/generate-from-clips", h.LegacyGenerateFromClips)
	r.POST("/generate-with-images", h.LegacyGenerateWithImages)
	r.POST("/generate-batch", h.LegacyGenerateBatch)
	r.POST("/curate", h.LegacyCurate)

	r.GET("/clips/search", h.SearchClipsByName)

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

// GetJobFullStatus is the Issue 9 / P2 (June 2026) thin
// delegator that forwards the /api/script/jobs/:job_id/full
// fetch to the canonical Jobs module handler. The
// composition root wires the JobsHandler.GetFull method
// through ScriptFlowDeps.JobFullStatus; the script route
// here only validates the port is wired (nil-guard) and
// then calls it. All the actual job-fetch + event-list +
// response-shape logic lives in *jobsapi.JobsHandler.GetFull
// (internal/api/jobs/impl.go) — zero duplication.
//
// Admin-token gate: the route group has
// `RequireAdminToken(h)` applied BEFORE this handler runs,
// so the admin-token protection is preserved (the Jobs
// module's own /api/jobs/:id/full route is admin-token-free;
// a naive 307 redirect here would bypass the gate; the
// delegator avoids that footgun).
func (h *ScriptFlowHandler) GetJobFullStatus(c *gin.Context) {
	if h.jobFullStatus == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs full status handler not initialized")
		return
	}
	h.jobFullStatus.GetFull(c)
}

func (h *ScriptFlowHandler) GetJobStatus(c *gin.Context) {
	if h.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}
	// Issue 9 / P2 followup (June 2026): the route is
	// registered as /jobs/:id (see registerJobRoutes), so
	// the param name is "id" not "job_id". Pre-Issue-9 the
	// script route used :job_id; the rename aligns with the
	// canonical JobsHandler route /api/jobs/:id contract.
	jobID := strings.TrimSpace(c.Param("id"))
	if jobID == "" {
		apiutil.BadRequest(c, "job id is required")
		return
	}
	job, err := h.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		apiutil.NotFound(c, fmt.Sprintf("job not found: %v", err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok": true, "job_id": job.ID, "status": job.Status,
		"progress": job.Progress, "error": job.Error, "result": job.Result,
	})
}
