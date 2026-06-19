package api

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/batch"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/association"
	"github.com/Marcuss-ops/PipelineGen/internal/media/images"
	"github.com/Marcuss-ops/PipelineGen/internal/media/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/content/mediacurator"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
)

type ScriptFlowHandler struct {
	generator         *ollama.Generator
	engine            *scripts.Engine
	batchService      *batch.BatchService
	imgService        *images.Service
	realtimeSvc       *realtime.Service
	associationSvc    *association.Service
	voService         *voiceover.Service
	assetTreeSvc      *assettree.Service
	groupsResolver    *voiceover.GroupsResolver
	clipSourceBuilder *scripts.ClipSourceBuilder
	mediaCurator      *mediacurator.Service
	insightBuilder    *ScriptInsightBuilder
	clipServices      ClipServices
	docClient         drive.DocClient
	driveUploader     *drive.Uploader
	jobsSvc           *jobservice.Service
	scriptsRepo       *scripts.ScriptRepository
	memorySvc         *gemmamemory.Service
	sourceResolver    *scripts.SourceResolver
	harvestSvc        AutoHarvestService
	driveFolderID     string
	cfg               *config.Config
	log               *zap.Logger
	metadataModel     string
}

// AutoHarvestService abstracts the clip harvest functionality.
type AutoHarvestService interface {
	EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (string, error)
}

func NewScriptFlowHandler(gen *ollama.Generator, engine *scripts.Engine, imgSvc *images.Service, realtimeSvc *realtime.Service, assocSvc *association.Service, voSvc *voiceover.Service, assetTreeSvc *assettree.Service, docClient drive.DocClient, driveUploader *drive.Uploader, jobsSvc *jobservice.Service, scriptsRepo *scripts.ScriptRepository, memorySvc *gemmamemory.Service, driveFolderID string, cfg *config.Config, log *zap.Logger) *ScriptFlowHandler {
	metaModel := strings.TrimSpace(cfg.External.OllamaModel)
	if mm := strings.TrimSpace(cfg.External.OllamaMetadataModel); mm != "" {
		metaModel = mm
	}

	artlistFolder := ""
	if cfg != nil {
		artlistFolder = cfg.Drive.ArtlistFolder()
	}

	// Build the ClipServices bundle from handler dependencies
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

	h := &ScriptFlowHandler{
		generator:      gen,
		engine:         engine,
		imgService:     imgSvc,
		realtimeSvc:    realtimeSvc,
		associationSvc: assocSvc,
		voService:      voSvc,
		assetTreeSvc:   assetTreeSvc,
		groupsResolver: groupsResolver,
		docClient:      docClient,
		driveUploader:  driveUploader,
		jobsSvc:        jobsSvc,
		scriptsRepo:    scriptsRepo,
		memorySvc:      memorySvc,
		driveFolderID:  driveFolderID,
		cfg:            cfg,
		log:            log,
		metadataModel:  metaModel,
		clipServices:   clipSvc,
		insightBuilder: &ScriptInsightBuilder{
			Logger:      log,
			MaxEntities: maxEntities,
			Services:    clipSvc,
		},
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

// SetClipSourceBuilder sets the ClipSourceBuilder for Clip→Script generation.
func (h *ScriptFlowHandler) SetClipSourceBuilder(b *scripts.ClipSourceBuilder) {
	h.clipSourceBuilder = b
}

// SetMediaCurator sets the MediaCurator for query-based compilation generation.
func (h *ScriptFlowHandler) SetMediaCurator(m *mediacurator.Service) {
	h.mediaCurator = m
}

func (h *ScriptFlowHandler) youTubeAwareSourceResolver() scripts.SourceTextResolver {
	return func(ctx context.Context, raw string) (string, string, error) {
		return batch.ResolveBatchSourceText(ctx, h.cfg, raw)
	}
}

// SetHarvestService sets the auto-harvest service for clip collection.
func (h *ScriptFlowHandler) SetHarvestService(svc AutoHarvestService) {
	h.harvestSvc = svc
	h.clipServices.HarvestSvc = svc
}

// SetBatchService sets the batch generation service.
func (h *ScriptFlowHandler) SetBatchService(svc *batch.BatchService) {
	h.batchService = svc
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
	r.POST("/generate-from-catalog", h.GenerateFromCatalog)
	r.POST("/curate", h.Curate)
	r.GET("/jobs/:job_id", h.GetJobStatus)
	r.GET("/jobs/:job_id/full", h.GetJobFullStatus)
	r.POST("/:id/sections/:section_id/regenerate", h.RegenerateSection)
	r.POST("/cache/evict", h.EvictCache)
}

// RegisterRoutesRemaining mounts the non-generation endpoints only.
// Generation routes (/generate-from-clips, /generate-with-images,
// /generate-batch) are handled by the thin Handler in api/script/.
//
// Active endpoints:
//   - POST /generate-from-catalog  — catalog query variant
//   - POST /curate                 — natural-language query → clip compilation
//   - GET  /jobs/:job_id           — job status lookup
//   - GET  /jobs/:job_id/full      — full job status
//   - POST /:id/sections/:section_id/regenerate — section regeneration
//   - POST /cache/evict            — cache eviction
func (h *ScriptFlowHandler) RegisterRoutesRemaining(r *gin.RouterGroup) {
	r.POST("/generate-from-catalog", h.GenerateFromCatalog)
	r.POST("/curate", h.Curate)
	r.GET("/jobs/:job_id", h.GetJobStatus)
	r.GET("/jobs/:job_id/full", h.GetJobFullStatus)
	r.POST("/:id/sections/:section_id/regenerate", h.RegenerateSection)
	r.POST("/cache/evict", h.EvictCache)
}

func (h *ScriptFlowHandler) resolveSourceText(ctx context.Context, raw string) (string, string, error) {
	if h.sourceResolver != nil {
		return h.youTubeAwareSourceResolver()(ctx, raw)
	}
	return batch.ResolveBatchSourceText(ctx, h.cfg, raw)
}
