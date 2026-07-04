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
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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
	scriptsRepo       adapters.ScriptRepository
	// Issue 4 (June 2026, P1): registry is the canonical job-type
	// Registry, attached at composition time so EnqueueGenerationJob
	// can source MaxRetries from registry.DefaultMaxRetries(jType)
	// instead of the pre-Issue-4 hard-coded 3-retry fallback.
	// Optional (nil-tolerant) so legacy test fixtures that don't
	// wire the registry keep working — EnqueueGenerationJob leaves
	// MaxRetries=0 in that case and the JobsService fallback (now
	// registry-aware) becomes the safety net.
	registry *appjobs.Registry
	// Commit H Phase 2 (June 2026): memorySvc field dropped (gemmamemory
	// gemmamemory wrapper gone).
	harvestSvc    AutoHarvestService
	driveFolderID string
	adminToken    string
	// PR-FIX (June 2026): lightweight clip-name searcher for
	// GET /script/clips/search?q= discovery endpoint.
	clipsSearcher ClipSearcher
	log           *zap.Logger
	// AZIONE 1 (July 2026): HandlerGenerate owns POST /generate.
	// Extracted from the 22-field God Object so the unified
	// generation endpoint carries only 3 fields (jobsSvc, log,
	// registry) instead of all 22.
	gen *HandlerGenerate
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

	ScriptsRepo adapters.ScriptRepository
	// Commit H Phase 2 (June 2026): Memory field dropped (gemmamemory
	// gemmamemory gate service gone).
	Jobs jobservice.Service
	// Issue 4 (June 2026, P1): optional canonical job-type registry
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
		scriptsRepo:       deps.ScriptsRepo,
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
		// AZIONE 1 (July 2026): construct the 3-field HandlerGenerate
		// alongside the 22-field ScriptFlowHandler. POST /generate
		// delegates to h.gen.Generate(c); legacy adapters call
		// h.enqueueEnvelope(c, env) which wraps enqueueEnvelopeFn.
		gen: NewHandlerGenerate(deps.Jobs, log, deps.Registry),
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

// registerJobRoutes mounts the canonical script job-status route.
// Blocco B (June 2026): /api/script/jobs/:id/full alias removed —
// the canonical route is /api/jobs/:id/full (mounted by the Jobs module).
func (h *ScriptFlowHandler) registerJobRoutes(r *gin.RouterGroup) {
	jobs := r.Group("")
	jobs.Use(RequireAdminToken(h))
	jobs.GET("/jobs/:id", h.GetJobStatus)
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
} // RegisterRoutes mounts every script-flow route under r. The unified
// generation endpoint (POST /generate) handles all generation modes.
// Legacy routes (generate-from-clips, generate-with-images, curate) are
// registered as deprecated adapters that translate old request formats to
// GenerationEnvelopeV2 and forward to the canonical enqueue.
// FASE 12c (July 2026): legacy batch route REMOVED.
// Flow routes (regenerate, evict, job status) are always mounted.
//
// AZIONE 1 (July 2026): POST /generate is handled by h.gen (HandlerGenerate,
// 3-field struct) instead of the 22-field ScriptFlowHandler.
func (h *ScriptFlowHandler) RegisterRoutes(r *gin.RouterGroup) {
	// Unified generation endpoint (replaces all legacy per-mode endpoints).
	h.gen.GenerateRoute(r)

	// Legacy routes — deprecated adapters (PR 11, June 2026).
	// Each translates the old request shape to GenerationEnvelopeV2,
	// enqueues as script.generate, and adds X-Deprecated: true header.
	r.POST("/generate-from-clips", h.LegacyGenerateFromClips)
	r.POST("/generate-with-images", h.LegacyGenerateWithImages)
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

// enqueueEnvelope is a thin wrapper around the package-level
// enqueueEnvelopeFn. Kept as a method on ScriptFlowHandler for
// backward compatibility with legacy adapter methods that call
// h.enqueueEnvelope(c, env). AZIONE 1 (July 2026): delegates to the
// extracted package-level function shared with HandlerGenerate.
func (h *ScriptFlowHandler) enqueueEnvelope(c *gin.Context, env domainScript.GenerationEnvelopeV2) {
	enqueueEnvelopeFn(c, env, h.jobsSvc, h.log, h.registry)
}

func (h *ScriptFlowHandler) GetJobStatus(c *gin.Context) {
	if h.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}
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
