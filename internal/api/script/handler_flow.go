// Package script (api/script) — ScriptFlowHandler is the canonical entry
// point for the script-flow HTTP surface. It owns the orchestration of
// batch generation, clip-source generation, job deltas, and section
// regeneration.
//
// PR4.F (June 2026) collapses the 22-positional-arg NewScriptFlowHandler
// constructor into a single ScriptFlowDeps value. The constructor body
// keeps the same side effects it always had:
//
//   - ClipServices bundle, InsightBuilder, semaphore, groups_resolver
//     are constructed internally from the deps.
//   - AssetTree is now first-class: no SetAssetTreeSvc late setter
//     (the deprecated setter is gone from this receiver).
//   - scripts.CurationService.SetClipSourceBuilder is still called
//     from this ctor body — the method is unrelated to ScriptFlowHandler
//     but is no longer reachable from outside the handler, so it is
//     effectively constructor-only.
//   - sourceResolver is built from the ollama client's WebSearcher.
//   - Generator.SetMetadataModel is invoked with the resolved model.
//
// The handler itself stays a "god object" by area (it still owns ~280
// lines of receiver methods covering generation, curation, jobs,
// status, and admin endpoints) — but its constructor no longer is
// one. New business logic should be added by spinning up a use case
// (e.g. scripts.SectionRegenerator, scripts.GenerateBatchUseCase) and
// wiring it through the deps struct, NOT by attaching setter methods
// to this receiver.
//
// PR4.F2 (June 2026) extracts the GenerateBatch orchestration into
// scripts.GenerateBatchUseCase. The handler method is now a thin
// transport that delegates to the use case.
package script

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/association"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// ScriptFlowHandler is the canonical handler. Fields are unexported;
// callers consume it via the script.Handler thin transport or the
// helpers exposed below (GetVoiceoverService, GetGroupsResolver,
// ResolveDriveFolderID, MaybeCreateGoogleDoc, ExecuteBatchGeneration,
// RegisterJobHandlers).
type ScriptFlowHandler struct {
	generator          *ollama.Generator
	engine             *scripts.Engine
	batchService       *scripts.BatchService
	curationService    *scripts.CurationService
	curationJobService CurationJobService
	catalogJobService  CatalogJobService
	imgService         *images.Service
	realtimeSvc        *realtime.Service
	associationSvc     *association.Service
	voService          *voiceover.Service
	assetTreeSvc       *assettree.Service
	groupsResolver     *voiceover.GroupsResolver
	clipSourceBuilder  *scripts.ClipSourceBuilder
	mediaCurator       *scripts.MediaCurator
	sectionRegen       *scripts.SectionRegenerator
	generateBatch      *scripts.GenerateBatchUseCase
	cacheEviction      *scripts.CacheEvictionUseCase
	insightBuilder     *ScriptInsightBuilder
	clipServices       ClipServices
	docClient          drive.DocClient
	driveUploader      *drive.Uploader
	jobsSvc            *jobservice.Service
	scriptsRepo        scripts.ScriptRepository
	memorySvc          *gemmamemory.Service
	sourceResolver     *scripts.SourceResolver
	harvestSvc         AutoHarvestService
	driveFolderID      string
	cfg                *config.Config
	log                *zap.Logger
	metadataModel      string

	// Semaphore for concurrent script generation, configured via ConcurrencyConfig.
	scriptGenSem chan struct{}
}

// AutoHarvestService abstracts the clip harvest functionality.
type AutoHarvestService interface {
	EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (string, error)
}

// ScriptFlowDeps groups all constructor inputs for NewScriptFlowHandler.
//
// PR4.F (June 2026): replaces the 22 positional args. Callers build this
// literal in app/registry.go, then call NewScriptFlowHandler(deps). The
// caller reads like a wire-list — name and zero-value each dependency —
// instead of guessing positional slot #17.
//
// Required: Generator, Engine, Cfg, Log.
//
// Optional (nil-safe): Batch, Curation, GenerateBatch, Section,
// Image, Realtime, Association, Voiceover, AssetTree,
// ClipSourceBuilder, MediaCurator, Harvest,
// CurationJobService, CatalogJobService, ScriptsRepo, Memory, Jobs,
// DocClient, DriveUploader, DriveScriptsGenFolder.
//
// If Section or GenerateBatch is nil, the corresponding endpoint returns
// 503. All other endpoints fall back to their own nil guards.
type ScriptFlowDeps struct {
	// Generation engine + service-level orchestrators.
	Generator *ollama.Generator
	Engine    *scripts.Engine

	// Use cases orchestrating the canonical sub-flows. Each is a
	// first-class dep object instantiated by the registry and consumed
	// by the handler via a single method (or small surface) per endpoint.
	Batch         *scripts.BatchService
	Curation      *scripts.CurationService
	Section       *scripts.SectionRegenerator
	GenerateBatch *scripts.GenerateBatchUseCase
	CacheEviction *scripts.CacheEvictionUseCase

	// Asset-side composability: these come from the SearchBundle /
	// AssetsBundle and feed the InsightBuilder + ClipServices bundle.
	Image       *images.Service
	Realtime    *realtime.Service
	Association *association.Service
	Voiceover   *voiceover.Service
	AssetTree   *assettree.Service

	// Asset curation primitives (optional; rebuilt in the ctor with deps).
	ClipSourceBuilder *scripts.ClipSourceBuilder
	MediaCurator      *scripts.MediaCurator
	Harvest           AutoHarvestService

	// Async job services (nullable; not wired today — see PR4.E comment in
	// registry.go). RegisterJobHandlers reads them with explicit nil-guards.
	CurationJobService CurationJobService
	CatalogJobService  CatalogJobService

	// Persistence + jobs.
	ScriptsRepo scripts.ScriptRepository
	Memory      *gemmamemory.Service
	Jobs        *jobservice.Service

	// Drive.
	DocClient             drive.DocClient
	DriveUploader         *drive.Uploader
	DriveScriptsGenFolder string

	// Meta.
	Cfg *config.Config
	Log *zap.Logger
}

// NewScriptFlowHandler constructs the canonical script-flow handler.
//
// See PR4.F (June 2026) above.
func NewScriptFlowHandler(deps ScriptFlowDeps) *ScriptFlowHandler {
	cfg := deps.Cfg
	log := deps.Log
	gen := deps.Generator

	// Resolve metadata model: cfg.External.OllamaMetadataModel (lighter
	// post-gen model) wins over the general OllamaModel.
	metaModel := strings.TrimSpace(cfg.External.OllamaModel)
	if mm := strings.TrimSpace(cfg.External.OllamaMetadataModel); mm != "" {
		metaModel = mm
	}

	artlistFolder := ""
	if cfg != nil {
		artlistFolder = cfg.Drive.ArtlistFolder()
	}

	// ClipServices bundle: shared by standalone helper functions in
	// flow_*.go (e.g. flow_clips_search.go) via the InsightBuilder.
	clipSvc := ClipServices{
		Logger:        log,
		RealtimeSvc:   deps.Realtime,
		AssocSvc:      deps.Association,
		DriveSvc:      deps.DriveUploader,
		Translator:    gen,
		JobsSvc:       deps.Jobs,
		ImgSvc:        deps.Image,
		VoSvc:         deps.Voiceover,
		ArtlistFolder: artlistFolder,
		MetadataModel: metaModel,
		HarvestSvc:    deps.Harvest,
	}

	maxEntities := 12
	if cfg != nil && cfg.Scripts.MaxInsightEntities > 0 {
		maxEntities = cfg.Scripts.MaxInsightEntities
	}

	// Topic → folder resolver from asset_tree. Nil-safe: a nil asset_tree
	// is acceptable because buildVoiceoverDestination handles nil and falls
	// back to Drive deep-search.
	var groupsResolver *voiceover.GroupsResolver
	if deps.AssetTree != nil {
		if gr, err := voiceover.NewGroupsResolver(deps.AssetTree, log); err != nil {
			log.Warn("ScriptFlowHandler groups_resolver not initialized (topic-by-DB routing disabled)",
				zap.Error(err))
		} else {
			groupsResolver = gr
		}
	} else {
		log.Info("ScriptFlowHandler groups_resolver disabled: assetTreeSvc nil (topic-by-DB routing disabled)")
	}

	maxScriptGen := cfg.Concurrency.MaxConcurrentScriptGenerations
	if maxScriptGen <= 0 {
		maxScriptGen = 1
	}

	h := &ScriptFlowHandler{
		generator:          gen,
		engine:             deps.Engine,
		batchService:       deps.Batch,
		curationService:    deps.Curation,
		curationJobService: deps.CurationJobService,
		catalogJobService:  deps.CatalogJobService,
		imgService:         deps.Image,
		realtimeSvc:        deps.Realtime,
		associationSvc:     deps.Association,
		voService:          deps.Voiceover,
		assetTreeSvc:       deps.AssetTree,
		groupsResolver:     groupsResolver,
		clipSourceBuilder:  deps.ClipSourceBuilder,
		mediaCurator:       deps.MediaCurator,
		sectionRegen:       deps.Section,
		generateBatch:      deps.GenerateBatch,
		cacheEviction:      deps.CacheEviction,
		docClient:          deps.DocClient,
		driveUploader:      deps.DriveUploader,
		jobsSvc:            deps.Jobs,
		scriptsRepo:        deps.ScriptsRepo,
		memorySvc:          deps.Memory,
		harvestSvc:         deps.Harvest,
		driveFolderID:      deps.DriveScriptsGenFolder,
		cfg:                cfg,
		log:                log,
		metadataModel:      metaModel,
		clipServices:       clipSvc,
		scriptGenSem:       make(chan struct{}, maxScriptGen),
		insightBuilder: &ScriptInsightBuilder{
			Logger:      log,
			MaxEntities: maxEntities,
			Services:    clipSvc,
		},
	}

	// Constructor side-effects that previously lived in
	// SetCurationClipSourceBuilder (now removed) and SetMetadataModel
	// setters: still happen here because no caller relies on these being
	// deferred.
	if deps.Curation != nil && deps.ClipSourceBuilder != nil {
		deps.Curation.SetClipSourceBuilder(deps.ClipSourceBuilder)
	}
	if gen != nil && gen.GetClient() != nil {
		ws := gen.GetClient().WebSearcher()
		h.sourceResolver = scripts.NewSourceResolver(h.youTubeAwareSourceResolver(), ws, log)
	}
	if gen != nil {
		gen.SetMetadataModel(metaModel)
	}
	return h
}

func (h *ScriptFlowHandler) youTubeAwareSourceResolver() scripts.SourceTextResolver {
	return func(ctx context.Context, raw string) (string, string, error) {
		return scripts.ResolveBatchSourceText(ctx, h.cfg, raw)
	}
}

// registerJobRoutes mounts the admin-gated /jobs/:job_id and
// /jobs/:job_id/full endpoints on the supplied router group.
//
// Generated by GenerateBatchUseCase to keep its async-dispatch handle
// addressable via the public JobStatus URLs.
//
// Kept private (lowercase r) because callers should compose handlers
// via RegisterRoutesRemaining; only the god-object decompose path
// invokes it internally. Once the god-object is fully split into
// per-capability handler types, each new handler will mount its own
// admin-gated sub-group inline, and `registerJobRoutes` will be deleted.
func (h *ScriptFlowHandler) registerJobRoutes(r *gin.RouterGroup) {
	jobs := r.Group("")
	jobs.Use(middleware.RequireAdminToken(h.cfg))
	jobs.GET("/jobs/:job_id", h.GetJobStatus)
	jobs.GET("/jobs/:job_id/full", h.GetJobFullStatus)
}

// RegisterRoutesRemaining mounts all non-generation script-flow endpoints.
//
// Generation routes (/generate-from-clips, /generate-with-images,
// /generate-batch, /generate-batch/progress) are handled by the thin
// Handler in api/script/handler.go (constructed from the same inner
// ScriptFlowHandler).
//
// Curation routes (/generate-from-catalog, /curate) delegate to the
// CurationService in application/scriptflow/curation/.
//
// Active endpoints:
//   - POST /generate-from-catalog  — catalog query variant (→ curationService)
//   - POST /curate                 — natural-language query → clip compilation (→ curationService)
//   - GET  /jobs/:job_id           — job status lookup (admin-gated, via registerJobRoutes)
//   - GET  /jobs/:job_id/full      — full job status (admin-gated, via registerJobRoutes)
//   - POST /:id/sections/:section_id/regenerate — section regeneration
//   - POST /cache/evict            — cache eviction
//
// PR4.F3 (June 2026): the job-status lookups are mounted on a sub-group
// carrying middleware.RequireAdminToken. The handler itself no longer
// owns any auth-state logic (the previous h.requireJobAuth method is
// retired — see handler_job_status.go's doc for the rationale).
//
// PR4.F5 (June 2026): the deprecated RegisterRoutes "full-fat"
// registration was deleted (zero callers in repo). The /jobs sub-group
// moved into a private helper (registerJobRoutes) so future god-object
// decomposition has a single import path.
func (h *ScriptFlowHandler) RegisterRoutesRemaining(r *gin.RouterGroup) {
	if h.curationService != nil {
		r.POST("/generate-from-catalog", h.curationService.GenerateFromCatalog)
		r.POST("/curate", h.curationService.Curate)
	}
	h.registerJobRoutes(r)
	r.POST("/:id/sections/:section_id/regenerate", h.RegenerateSection)
	r.POST("/cache/evict", h.EvictCache)
}

func (h *ScriptFlowHandler) resolveSourceText(ctx context.Context, raw string) (string, string, error) {
	if h.sourceResolver != nil {
		return h.youTubeAwareSourceResolver()(ctx, raw)
	}
	return scripts.ResolveBatchSourceText(ctx, h.cfg, raw)
}

// GetVoiceoverService returns the voiceover service for wiring job services.
func (h *ScriptFlowHandler) GetVoiceoverService() *voiceover.Service {
	return h.voService
}

// GetGroupsResolver returns the groups resolver for wiring job services.
func (h *ScriptFlowHandler) GetGroupsResolver() *voiceover.GroupsResolver {
	return h.groupsResolver
}

// ResolveDriveFolderID delegates to the unexported resolver used by job services.
func (h *ScriptFlowHandler) ResolveDriveFolderID(ctx context.Context, input, defaultRootID string) (string, error) {
	return h.resolveDriveFolderID(ctx, input, defaultRootID)
}

// MaybeCreateGoogleDoc creates a Google Doc via the documents service.
func (h *ScriptFlowHandler) MaybeCreateGoogleDoc(ctx context.Context, title, content, folderID string, createDoc bool) (string, string) {
	if !createDoc {
		return "", ""
	}
	docsSvc := scripts.NewDocumentsService(h.docClient, h.log, h.driveFolderID)
	return docsSvc.CreateDoc(ctx, title, content, h.resolveDriveFolderID, folderID)
}

// GetJobFullStatus returns the full job state including events.
//
// Auth: middleware.RequireAdminToken (mounted at route registration
// in RegisterRoutesRemaining). The handler itself does NOT re-check
// the credential — if a request reaches this method without a valid
// admin token, the middleware already wrote the 401 response and
// aborted the chain.
//
// PR3 (June 2026): moved from internal/api/script/handler_job_status.go
// into this file as part of the api/script 25→8 file compaction.
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
		"ok":             true,
		"job_id":         job.ID,
		"type":           job.Type,
		"status":         job.Status,
		"priority":       job.Priority,
		"progress":       job.Progress,
		"error":          job.Error,
		"result":         job.Result,
		"retry_count":    job.RetryCount,
		"max_retries":    job.MaxRetries,
		"correlation_id": job.CorrelationID,
		"created_at":     job.CreatedAt,
		"started_at":     job.StartedAt,
		"completed_at":   job.CompletedAt,
		"updated_at":     job.UpdatedAt,
		"events":         events,
	})
}

// GetJobStatus returns the lightweight job state (status/progress/error/result).
//
// Auth: see GetJobFullStatus's doc comment — applied at the route layer.
//
// PR3 (June 2026): moved from internal/api/script/handler_job_status.go.
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
		"ok":       true,
		"job_id":   job.ID,
		"status":   job.Status,
		"progress": job.Progress,
		"error":    job.Error,
		"result":   job.Result,
	})
}

// ExecuteBatchGeneration is a thin wrapper that delegates the batch
// generation request to the underlying *scripts.BatchService. It exists
// so tests and API code can call batch generation through the unified
// ScriptFlowHandler receiver instead of constructing a BatchService
// directly. Returns the canonical BatchGenerateResponse.
func (h *ScriptFlowHandler) ExecuteBatchGeneration(ctx context.Context, req *scripts.GenerateBatchRequest, onProgress func(int, string)) (scripts.BatchGenerateResponse, error) {
	if h.batchService == nil {
		return scripts.BatchGenerateResponse{}, fmt.Errorf("batch service not initialized on ScriptFlowHandler")
	}
	return h.batchService.ExecuteBatchGeneration(ctx, req, onProgress)
}
