// Package app — wire_script.go canonicalises the ScriptFlow module wiring
// outside of the monolithic registry.go.
//
// Agente 4 — H (June 2026): extracted from registry.go. ClipServices is
// pre-built here (access to concrete infrastructure) and passed to the
// handler. Job registration happens at composition time.

package app

import (
	"context"
	"strings"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"

	artlistpkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"

	"go.uber.org/zap"
)

// wireScriptFlow constructs and registers the ScriptFlow module.
func wireScriptFlow(ctx context.Context, cfg *config.Config, log *zap.Logger, root *ComposeRoot, registry *module.Registry) {
	if root.AI == nil || root.AI.ScriptGen == nil || root.Domains == nil || root.Domains.ImageService == nil {
		return
	}

	memorySvc := root.AI.MemoryService
	engine := root.AI.ScriptEngine
	gen := root.AI.ScriptGen

	if memorySvc == nil || engine == nil {
		log.Warn("wireScriptFlow: AIBundle services not fully initialized — skipping ScriptFlow")
		return
	}

	scriptsRepoAdapter := scripts.NewRepositoryAdapter(root.Repos.ScriptsRepo)
	batchSvc := scripts.NewBatchService(cfg, log, gen, engine, root.Drive.DocClient, root.Domains.VoiceoverService, scriptsRepoAdapter)
	curationSvc := scripts.NewCurationService(nil, root.Jobs.Service, log)

	// ── Clip source builder ────────────────────────────────────────────
	var clipSourceBuilder *scripts.ClipSourceBuilder
	if ollamaClient := gen.GetClient(); ollamaClient != nil {
		clipSourceBuilder = scripts.NewClipSourceBuilder(root.Repos.ClipsRepo, ollamaClient, log)
		if root.Process.VectorSvc != nil && cfg.Features.CatalogScriptVectorSearch {
			clipSourceBuilder.SetVectorStore(qdrant.NewSearchAdapter(root.Process.VectorSvc))
		}
		if cfg.Reranker.Enabled {
			clipSourceBuilder.SetReranker(reranker.NewClient(reranker.Config{
				Enabled:   cfg.Reranker.Enabled,
				URL:       cfg.Reranker.URL,
				Model:     cfg.Reranker.Model,
				TopK:      cfg.Reranker.TopK,
				TimeoutMs: cfg.Reranker.TimeoutMs,
			}))
		}
	}

	// ── Media curator ──────────────────────────────────────────────────
	var mediaCurator *scripts.MediaCurator
	if (root.Process.VectorSvc != nil || root.Repos.ClipsRepo != nil) && engine != nil {
		mediaCurator = scripts.NewMediaCurator(qdrant.NewSearchAdapter(root.Process.VectorSvc), cfg.ClipIndexer.ServerURL, root.Repos.ClipsRepo, clipSourceBuilder, engine, log)
	}

	// ── Harvest service ────────────────────────────────────────────────
	var harvestSvc scriptapi.AutoHarvestService
	if root.Jobs.Service != nil {
		presetsConfig, _ := artlistpkg.LoadPresets("config/presets.yaml")
		harvestSvc = clipresolver.NewJobHarvestService(root.Jobs.Facade, log, presetsConfig, cfg.Drive.ArtlistFolder())
	}

	// ── Pre-built ClipServices (avoids infrastructure imports in api/script) ──
	metaModel := strings.TrimSpace(cfg.External.OllamaModel)
	if mm := strings.TrimSpace(cfg.External.OllamaMetadataModel); mm != "" {
		metaModel = mm
	}
	artlistFolder := cfg.Drive.ArtlistFolder()
	clipServices := scripts.ClipServices{
		Logger:        log,
		RealtimeSvc:   root.Domains.RealtimeService,
		AssocSvc:      root.Domains.AssocService,
		DriveSvc:      root.Drive.DriveUploader,
		Translator:    gen,
		JobsSvc:       root.Jobs.Facade,
		ImgSvc:        root.Domains.ImageService,
		VoSvc:         root.Domains.VoiceoverService,
		ArtlistFolder: artlistFolder,
		MetadataModel: metaModel,
		HarvestSvc:    harvestSvc,
	}

	// ── Drive folder client adapter ────────────────────────────────────
	driveFolderClient := &driveFolderAdapterImpl{
		uploader: root.Drive.DriveUploader,
	}

	// ── Document creator adapter ───────────────────────────────────────
	documentCreator := &docCreatorImpl{
		docClient:     root.Drive.DocClient,
		log:           log,
		driveFolderID: cfg.Drive.ScriptsGenFolder(),
	}

	// ── Admin token ────────────────────────────────────────────────────
	adminToken := ""
	if cfg != nil {
		adminToken = cfg.Security.AdminToken
	}

	// ── Use cases ──────────────────────────────────────────────────────
	sectionRegen := scripts.NewSectionRegenerator(
		scriptsRepoAdapter, gen,
		root.Drive.DocClient, cfg, log,
	)
	generateBatchUC := scripts.NewGenerateBatchUseCase(
		cfg, log, root.Jobs.Facade, batchSvc,
		cfg.Drive.ScriptsGenFolder(),
	)
	cacheEvictionUC := scripts.NewCacheEvictionUseCase(
		gen, memorySvc, log,
	)

	// ── Pipeline use case ──────────────────────────────────────────────
	var pipelineUC *scripts.PipelineUseCase
	var semUC *scripts.SemaphoreUseCase
	var prewarmUC *scripts.PrewarmUseCase

	if root.AI.ScriptEngine != nil && engine != nil {
		maxScriptGen := 1
		if cfg != nil && cfg.Concurrency.MaxConcurrentScriptGenerations > 0 {
			maxScriptGen = cfg.Concurrency.MaxConcurrentScriptGenerations
		}
		if uc, err := scripts.NewSemaphoreUseCase(maxScriptGen, log); err == nil {
			semUC = uc
		} else {
			log.Warn("pipeline use case: semaphore init failed", zap.Error(err))
		}

		var prewarmSvc scripts.PrewarmImageService
		if root.Domains != nil {
			prewarmSvc = root.Domains.ImageService
		}
		prewarmUC = scripts.NewPrewarmUseCase(prewarmSvc, log)

		scenesUC := scripts.NewSceneBuilderUseCase(
			prewarmSvc, root.Domains.VoiceoverService, log, cfg,
			nil, nil,
		)
		docsUC := scripts.NewDocumentsUseCase(root.Drive.DocClient, log, cfg.Drive.ScriptsGenFolder())

		// Agente 4 — J: use composition context
		var scenesSvc *scripts.ScenesService
		if scenesUC != nil {
			if sv, err := scenesUC.Build(ctx); err == nil {
				scenesSvc = sv
			} else {
				log.Warn("pipeline use case: scene builder init failed", zap.Error(err))
			}
		}
		var docsSvc *scripts.DocumentsService
		if docsUC != nil {
			docsSvc = docsUC.DocumentsService()
		}

		// ── PostGenUseCase (Agente 4 — F, G) ──────────────────────────
		postGenMetaModel := metaModel
		postGenArtlistFolder := artlistFolder
		postGenInsightBuilder := &scripts.ScriptInsightBuilder{
			Logger:      log,
			MaxEntities: 12,
			Services: scripts.ClipServices{
				Logger:        log,
				RealtimeSvc:   root.Domains.RealtimeService,
				AssocSvc:      root.Domains.AssocService,
				DriveSvc:      root.Drive.DriveUploader,
				Translator:    gen,
				JobsSvc:       root.Jobs.Facade,
				ImgSvc:        root.Domains.ImageService,
				VoSvc:         root.Domains.VoiceoverService,
				ArtlistFolder: postGenArtlistFolder,
				MetadataModel: postGenMetaModel,
				HarvestSvc:    harvestSvc,
			},
		}
		var postGenExtractor scripts.EntityScriptExtractor
		if gen != nil {
			postGenExtractor = gen.GetClient()
		}
		postGenUC := scripts.NewPostGenUseCase(
			postGenExtractor, postGenInsightBuilder, gen, postGenMetaModel, log,
		)
		postGen := func(ctx context.Context, spec *scriptpkg.GenerationSpec, scr string) (entitiesJSON string, insights any, videoMetadata []scripts.VideoMetadata) {
			res, err := postGenUC.Run(ctx, spec, scr)
			if err != nil {
				log.Warn("post-gen use case error (continuing with partial results)", zap.Error(err))
			}
			return res.EntitiesJSON, res.Insights, res.VideoMetadata
		}

		if scenesSvc != nil && docsSvc != nil {
			pipeline := scripts.NewPipeline(log, "", scenesSvc, docsSvc, postGen, nil)
			minFloor := 100
			ollamaModel := ""
			if cfg != nil {
				if cfg.Scripts.MinWordFloor > 0 {
					minFloor = cfg.Scripts.MinWordFloor
				}
				ollamaModel = cfg.External.OllamaModel
			}
			pu, puErr := scripts.NewPipelineUseCase(
				log, root.AI.ScriptEngine,
				minFloor, ollamaModel,
				clipSourceBuilder,
				mediaCurator,
				semUC, prewarmUC,
				pipeline,
			)
			if puErr != nil {
				log.Warn("pipeline use case: construction failed", zap.Error(puErr))
			} else {
				pipelineUC = pu
			}
		}
	}

	// ── Construct handler ──────────────────────────────────────────────
	handler := scriptapi.NewScriptFlowHandler(scriptapi.ScriptFlowDeps{
		Engine:                engine,
		Batch:                 batchSvc,
		Curation:              curationSvc,
		Section:               sectionRegen,
		GenerateBatch:         generateBatchUC,
		CacheEviction:         cacheEvictionUC,
		PipelineUseCase:       pipelineUC,
		Image:                 root.Domains.ImageService,
		Realtime:              root.Domains.RealtimeService,
		Association:           root.Domains.AssocService,
		Voiceover:             root.Domains.VoiceoverService,
		AssetTree:             root.Search.AssetTreeService,
		ClipSourceBuilder:     clipSourceBuilder,
		MediaCurator:          mediaCurator,
		Harvest:               harvestSvc,
		CurationJobService:    nil,
		CatalogJobService:     nil,
		ScriptsRepo:           scriptsRepoAdapter,
		Memory:                memorySvc,
		Jobs:                  root.Jobs.Facade,
		AdminToken:            adminToken,
		DriveFolderClient:     driveFolderClient,
		DocumentCreator:       documentCreator,
		DriveScriptsGenFolder: cfg.Drive.ScriptsGenFolder(),
		ClipServices:          clipServices,
		Log:                   log,
	})

	// ── Register job handlers at composition time ──────────────────────
	if root.Jobs.Service != nil {
		if batchSvc != nil {
			root.Jobs.Service.RegisterHandler("script.generate_batch", handler.HandleBatchScriptGenerateJob)
			log.Info("registered script.generate_batch job handler (wire_script.go)")
		}
		if pipelineUC != nil {
			if err := pipelineUC.RegisterJobs(root.Jobs.Service); err != nil {
				log.Warn("pipeline use case job registration failed", zap.Error(err))
			}
		}
	}

	// ── Set metadata model + build source resolver ────────────────────
	if gen != nil {
		gen.SetMetadataModel(metaModel)
	}

	// ── Register HTTP module ───────────────────────────────────────────
	genSvc := scripts.NewGenerationService(root.Jobs.Facade, cfg, log)
	mod := module.NewRouteModule(
		"script-flow",
		func() bool { return cfg.Features.ScriptDocsEnabled },
		"/script",
		scriptapi.NewHandler(handler, genSvc),
		log,
		module.WithMiddleware(middleware.ScriptDocsEnabled(cfg)),
	)
	registerModule(registry, log, mod)
}

// ── Adapters ────────────────────────────────────────────────────────────────

// driveFolderAdapterImpl wraps *drive.Uploader as scriptapi.DriveFolderClient.
type driveFolderAdapterImpl struct {
	uploader *drive.Uploader
}

func (a *driveFolderAdapterImpl) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if a == nil || a.uploader == nil {
		return "", nil
	}
	return a.uploader.GetOrCreateFolder(ctx, name, parentID)
}

// docCreatorImpl wraps drive.DocClient as scriptapi.DocumentCreator.
type docCreatorImpl struct {
	docClient     drive.DocClient
	log           *zap.Logger
	driveFolderID string
}

func (d *docCreatorImpl) CreateDoc(ctx context.Context, title, content, folderID string) (string, string) {
	if d == nil || d.docClient == nil {
		return "", ""
	}
	docsSvc := scripts.NewDocumentsService(d.docClient, d.log, d.driveFolderID)
	// Folder resolution: if folderID looks like a raw Drive ID, use it directly;
	// otherwise, treat it as a path and try get-or-create (delegates to uploader).
	resolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
		return input, nil // raw ID assumed (caller resolved beforehand)
	}
	return docsSvc.CreateDoc(ctx, title, content, resolveFolder, folderID)
}
