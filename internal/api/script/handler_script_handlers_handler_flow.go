package script

import (
	"fmt"

	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/association"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

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

// NewScriptFlowHandler constructs the canonical script-flow handler.
//
// PR4-H (June 2026) absorbed the SetBatchService + SetCurationService
// post-construction setters in Commit 5 — batch + curation services are now
// constructor-injected.
//
// PR4.E (June 2026) absorbs the 4 remaining post-construction setters into
// the ctor as well: clipSourceBuilder, mediaCurator, harvestSvc, and the
// nil-safe curationJobService + catalogJobService. The ctor grows from 17
// to 22 args. Side effects that SetCurationClipSourceBuilder used to perform
// (forwarding the builder to the curation service) now happen inside the
// ctor body. All 6 post-construction setters are removed.
//
// curationJobService + catalogJobService remain part of the ctor signature
// even though no caller wires them today — RegisterJobHandlers reads them
// with explicit nil-guards, so passing nil is safe and preserves the option
// for future modules to inject them without re-introducing a setter.
func NewScriptFlowHandler(
	gen *ollama.Generator,
	engine *scripts.Engine,
	imgSvc *images.Service,
	realtimeSvc *realtime.Service,
	assocSvc *association.Service,
	voSvc *voiceover.Service,
	assetTreeSvc *assettree.Service,
	docClient drive.DocClient,
	driveUploader *drive.Uploader,
	jobsSvc *jobservice.Service,
	scriptsRepo scripts.ScriptRepository,
	memorySvc *gemmamemory.Service,
	driveFolderID string,
	cfg *config.Config,
	log *zap.Logger,
	batchSvc *scripts.BatchService,
	curationSvc *scripts.CurationService,
	clipSourceBuilder *scripts.ClipSourceBuilder,
	mediaCurator *scripts.MediaCurator,
	harvestSvc AutoHarvestService,
	curationJobService CurationJobService,
	catalogJobService CatalogJobService,
) *ScriptFlowHandler {
	metaModel := strings.TrimSpace(cfg.External.OllamaModel)
	if mm := strings.TrimSpace(cfg.External.OllamaMetadataModel); mm != "" {
		metaModel = mm
	}

	artlistFolder := ""
	if cfg != nil {
		artlistFolder = cfg.Drive.ArtlistFolder()
	}

	// Build the ClipServices bundle from handler dependencies.
	// PR4.E: harvestSvc is set directly in the literal here (replaces
	// SetHarvestService's side effect on h.clipServices.HarvestSvc).
	clipSvc := ClipServices{
		Logger:        log,
		RealtimeSvc:   realtimeSvc,
		AssocSvc:      assocSvc,
		DriveSvc:      driveUploader,
		Translator:    gen,
		JobsSvc:       jobsSvc,
		ImgSvc:        imgSvc,
		VoSvc:         voSvc,
		ArtlistFolder: artlistFolder,
		MetadataModel: metaModel,
		HarvestSvc:    harvestSvc,
	}

	maxEntities := 12
	if cfg != nil && cfg.Scripts.MaxInsightEntities > 0 {
		maxEntities = cfg.Scripts.MaxInsightEntities
	}

	// Build topic→folder resolver from asset_tree. Best-effort: nil
	// resolver is acceptable; buildVoiceoverDestination handles nil
	// gracefully (skips DB lookup, falls back to Drive deep-search).
	var groupsResolver *voiceover.GroupsResolver
	if assetTreeSvc != nil {
		if gr, err := voiceover.NewGroupsResolver(assetTreeSvc, log); err != nil {
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
		engine:             engine,
		batchService:       batchSvc,
		curationService:    curationSvc,
		curationJobService: curationJobService,
		catalogJobService:  catalogJobService,
		imgService:         imgSvc,
		realtimeSvc:        realtimeSvc,
		associationSvc:     assocSvc,
		voService:          voSvc,
		assetTreeSvc:       assetTreeSvc,
		groupsResolver:     groupsResolver,
		clipSourceBuilder:  clipSourceBuilder,
		mediaCurator:       mediaCurator,
		docClient:          docClient,
		driveUploader:      driveUploader,
		jobsSvc:            jobsSvc,
		scriptsRepo:        scriptsRepo,
		memorySvc:          memorySvc,
		harvestSvc:         harvestSvc,
		driveFolderID:      driveFolderID,
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
	// PR4.E: absorb the side-effect of the removed SetCurationClipSourceBuilder.
	// The ctor is the only point where this forward happens; downstream code
	// reads it from curationService.ClipSourceBuilder indirectly via calls.
	if curationSvc != nil && clipSourceBuilder != nil {
		curationSvc.SetClipSourceBuilder(clipSourceBuilder)
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

// SetAssetTreeSvc sets the asset-tree service after construction. Used by
// app wiring paths that build the ScriptFlowHandler before assetTreeSvc is
// ready. Triggers a groups_resolver rebuild.
func (h *ScriptFlowHandler) SetAssetTreeSvc(svc *assettree.Service) {
	h.assetTreeSvc = svc
	if svc == nil {
		h.groupsResolver = nil
		return
	}
	if gr, err := voiceover.NewGroupsResolver(svc, h.log); err == nil {
		h.groupsResolver = gr
	} else if h.log != nil {
		h.log.Warn("rebuild groups_resolver failed", zap.Error(err))
	}
}

func (h *ScriptFlowHandler) youTubeAwareSourceResolver() scripts.SourceTextResolver {
	return func(ctx context.Context, raw string) (string, string, error) {
		return scripts.ResolveBatchSourceText(ctx, h.cfg, raw)
	}
}

// RegisterRoutes mounts ALL script generation endpoints (full-fat registration).
//
// DEPRECATED: PR 2-3 moved generation routes to api/script/handler.go.
// This method remains for backward compatibility with callers that register
// ScriptFlowHandler directly (e.g., tests). Use RegisterRoutesRemaining for
// the new split routing.
func (h *ScriptFlowHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate-batch", h.GenerateBatch)
	r.GET("/generate-batch/progress", h.GetBatchProgress)
	r.GET("/jobs/:job_id", h.GetJobStatus)
	r.GET("/jobs/:job_id/full", h.GetJobFullStatus)
	r.POST("/:id/sections/:section_id/regenerate", h.RegenerateSection)
	r.POST("/cache/evict", h.EvictCache)
}

// RegisterRoutesRemaining mounts the non-generation endpoints only.
// Generation routes (/generate-from-clips, /generate-with-images,
// /generate-batch) are handled by the thin Handler in api/script/.
// Curation routes (/generate-from-catalog, /curate) delegate to the
// CurationService in application/scriptflow/curation/.
//
// Active endpoints:
//   - POST /generate-from-catalog  — catalog query variant (→ curationService)
//   - POST /curate                 — natural-language query → clip compilation (→ curationService)
//   - GET  /jobs/:job_id           — job status lookup
//   - GET  /jobs/:job_id/full      — full job status
//   - POST /:id/sections/:section_id/regenerate — section regeneration
//   - POST /cache/evict            — cache eviction
func (h *ScriptFlowHandler) RegisterRoutesRemaining(r *gin.RouterGroup) {
	if h.curationService != nil {
		r.POST("/generate-from-catalog", h.curationService.GenerateFromCatalog)
		r.POST("/curate", h.curationService.Curate)
	}
	r.GET("/jobs/:job_id", h.GetJobStatus)
	r.GET("/jobs/:job_id/full", h.GetJobFullStatus)
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
