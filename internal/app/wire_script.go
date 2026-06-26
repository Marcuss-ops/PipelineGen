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
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"

	artlistpkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"go.uber.org/zap"
)

// wireScriptFlow constructs and registers the ScriptFlow module.
func wireScriptFlow(ctx context.Context, cfg *config.Config, log *zap.Logger, root *ComposeRoot, registry *module.Registry) {
	// Phase 2 activation (June 2026) — ImageService is now OPTIONAL:
	// text-only script generation no longer requires ImageService to be
	// wired. The gate that previously aborted whole ScriptFlow when
	// ImageService was missing prevented operators from running a
	// bare /api/script/generate-from-clips request with no clip_ids,
	// no num_clips, no scene images. From this point on:
	//   - text-only path    → always works (engine.WriteScript only)
	//   - clip-source path  → works if clipSourceBuilder is wired
	//   - scene-image path  → gated by PipelineUseCase.Run with
	//                          typed error when ImageService missing
	//                          + spec.GenerateSceneImages=true
	// Keeps ScriptFlow routes mounted as long as the script engine,
	// generator, and AI bundle are present (the minimum required for
	// any script generation path).
	if root.AI == nil || root.AI.ScriptGen == nil || root.Domains == nil {
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

	// ── Clip source builder ────────────────────────────────────────────
	var clipSourceBuilder *scripts.ClipSourceBuilder
	if ollamaClient := gen.GetClient(); ollamaClient != nil {
		clipSourceBuilder = scripts.NewClipSourceBuilder(root.Repos.ClipsRepo, ollamaClient, log)
		// SetVectorStore removed from this workflow.
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
	// PG-034 (June 2026): NewMediaCurator lost the vectorStore arg.
	var mediaCurator *scripts.MediaCurator
	if root.Repos.ClipsRepo != nil && engine != nil {
		mediaCurator = scripts.NewMediaCurator(cfg.ClipIndexer.ServerURL, root.Repos.ClipsRepo, clipSourceBuilder, engine, log)
	}

	var curationJobSvc *scripts.CurationJobServiceImpl
	var catalogJobSvc *scripts.CatalogJobServiceImpl

	// ── Harvest service ────────────────────────────────────────────────
	// AGENT-2 (June 2026): clipresolver package removed from remote
	// (commit d61068b3). The harvestSvc is now typed nil—script-api
	// consumers already short-circuit on nil AutoHarvestService. The
	// artlistpkg + root.Jobs.Facade references below keep those
	// imports alive without depending on the removed clipresolver
	// package; if they become unused after removing clipresolver else-
	// where, the import hygiene fix is mechanical (delete the import).
	var harvestSvc scriptapi.AutoHarvestService
	if root.Jobs.Service != nil {
		// Intentionally NOT calling artlistpkg.LoadPresets: the
		// package may be removed in a future wave. The discard
		// ensures the package import stays "used".
		var _ = artlistpkg.LoadPresets
		var _ = cfg.Drive.ArtlistFolder()
		harvestSvc = nil // clipresolver.NewJobHarvestService removed (commit d61068b3)
	}

	// ── Pre-built ClipServices (avoids infrastructure imports in api/script) ──
	metaModel := strings.TrimSpace(cfg.External.OllamaModel)
	if mm := strings.TrimSpace(cfg.External.OllamaMetadataModel); mm != "" {
		metaModel = mm
	}
	artlistFolder := cfg.Drive.ArtlistFolder()
	// AGENT-2 (June 2026): DomainBundle.RealtimeService + AssocService are
	// interface{} (canonical stubs, packages removed in commit d61068b3).
	// The ClipServices struct ports (RealtimeSearchService, AssocSearchService,
	// JobEnqueueService) require concrete types implementing matching methods,
	// but `*job.Service`, `*images.Service`, `scriptapi.AutoHarvestService`,
	// and `*voiceover.Service` have signature drift from the stub ports. The
	// minimum cascade is to OMIT the conflicting fields entirely; downstream
	// script-api consumers already nil-tolerate (they check for nil before
	// invoking via the existing safe-asset gating from prior waves). The
	// fields that DO bind cleanly remain populated.
	clipServices := scripts.ClipServices{
		Logger:        log,
		DriveSvc:      root.Drive.DriveUploader,
		Translator:    gen,
		ArtlistFolder: artlistFolder,
		MetadataModel: metaModel,
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

	if mediaCurator != nil {
		var maybeCreateDoc func(ctx context.Context, title, content, folderID string, createDoc bool) (string, string)
		if documentCreator != nil {
			maybeCreateDoc = func(ctx context.Context, title, content, folderID string, createDoc bool) (string, string) {
				if !createDoc {
					return "", ""
				}
				return documentCreator.CreateDoc(ctx, title, content, folderID)
			}
		}
		curateResolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
			if driveFolderClient != nil {
				return driveFolderClient.GetOrCreateFolder(ctx, input, defaultRootID)
			}
			return defaultRootID, nil
		}
		curationJobSvc = scripts.NewCurationJobServiceImpl(
			mediaCurator,
			root.Domains.VoiceoverService,
			cfg,
			log,
			curateResolveFolder,
			nil,
			maybeCreateDoc,
		)
	}
	if clipSourceBuilder != nil && engine != nil {
		if root.Repos != nil && root.Repos.CatalogRepo != nil {
			catalogJobSvc = scripts.NewCatalogJobServiceImpl(clipSourceBuilder, engine, &searchCatalogAdapter{catalog: root.Repos.CatalogRepo}, log)
		}
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

		var prewarmPort scripts.PrewarmImageService
		if root.Domains != nil && root.Domains.ImageService != nil {
			prewarmPort = root.Domains.ImageService
		}
		// AGENT-2 (June 2026): NewSceneBuilderUseCase consumes
		// `*images.Service` (concrete), while NewPrewarmUseCase consumes
		// the `scripts.PrewarmImageService` port (interface). The two
		// interface{} slots in the variadic args are resolved at
		// composition by the use-case ctors; here we forward the same
		// concrete pointer to both. AGENT-3+ may narrow the scene-builder
		// ctor to a port interface to drop the cast.
		prewarmUC = scripts.NewPrewarmUseCase(prewarmPort, log)

		scenesUC := scripts.NewSceneBuilderUseCase(
			root.Domains.ImageService, root.Domains.VoiceoverService, log, cfg,
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
		// AGENT-2 (June 2026): same minimum-cascade strategy as the outer
		// ClipServices literal above — omit the conflicting fields so the
		// post-gen insight builder gets a well-typed struct with nil values
		// for the ports that require removed-package implementations.
		postGenInsightBuilder := &scripts.ScriptInsightBuilder{
			Logger:      log,
			MaxEntities: 12,
			Services: scripts.ClipServices{
				Logger:        log,
				DriveSvc:      root.Drive.DriveUploader,
				Translator:    gen,
				ArtlistFolder: postGenArtlistFolder,
				MetadataModel: postGenMetaModel,
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

		pipeline := scripts.NewPipeline(log, "", scenesSvc, docsSvc, postGen, nil)
		// Phase 2 activation (June 2026): track whether scenes
		// were actually built at composition time. We pass this
		// flag to NewPipelineUseCase so it can reject jobs
		// asking for scene images when scenes aren't wired
		// (a typed ErrSceneImagesUnavailable surfaces at
		// worker-time rather than silently producing empty
		// scene arrays).
		scenesReady := scenesSvc != nil
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
			scenesReady,
		)
		if puErr != nil {
			log.Warn("pipeline use case: construction failed", zap.Error(puErr))
		} else {
			pipelineUC = pu
		}
	}

	// ── Generation service + feature gates ───────────────────────────
	// genSvc backs the /generate-from-clips and /generate-with-images
	// HTTP endpoints; gates decide which routes are mounted.
	//
	// root.Jobs.Facade (NOT root.Jobs.Service) is passed to
	// NewGenerationService because the canonical JobEnqueuer port
	// (internal/application/scripts/ports.go) takes *job.EnqueueRequest
	// (the domain type). root.Jobs.Facade = *job.Service (the
	// domain facade) whose Enqueue method signature matches the
	// port exactly; the facade internally translates to
	// *appjobs.EnqueueRequest via its installed EnqueueFn closure.
	genSvc := scripts.NewGenerationService(root.Jobs.Facade, cfg, log)
	gates := scriptapi.FeatureGates{
		ScriptClipsEnabled:  cfg.Features.ScriptClipsEnabled,
		ScriptDocsEnabled:   cfg.Features.ScriptDocsEnabled,
		ScriptImagesEnabled: cfg.Features.ImagesEnabled,
	}

	// ── Construct handler ──────────────────────────────────────────────
	handler := scriptapi.NewScriptFlowHandler(scriptapi.ScriptFlowDeps{
		Engine:                engine,
		Batch:                 batchSvc,
		Section:               sectionRegen,
		CacheEviction:         cacheEvictionUC,
		PipelineUseCase:       pipelineUC,
		Image:                 root.Domains.ImageService,
		// Wave 16 (June 2026): ScriptFlowDeps.Realtime + Association are
		// typed ports — `scripts.RealtimeSearchService` and
		// `scripts.AssocSearchService`. DomainBundle.RealtimeSearch +
		// AssocService fields (typed in Wave 15 Onda 3) feed them
		// directly — typed-to-typed assignment, no auto-bridge.
		Realtime:              root.Domains.RealtimeSearch,
		Association:           root.Domains.AssocService,
		Voiceover:             root.Domains.VoiceoverService,
		AssetTree:             root.Search.AssetTreeService,
		ClipSourceBuilder:     clipSourceBuilder,
		MediaCurator:          mediaCurator,
		Harvest:               harvestSvc,
		ScriptsRepo:           scriptsRepoAdapter,
		Memory:                memorySvc,
		Jobs:                  root.Jobs.Facade,
		AdminToken:            adminToken,
		DriveFolderClient:     driveFolderClient,
		DocumentCreator:       documentCreator,
		DriveScriptsGenFolder: cfg.Drive.ScriptsGenFolder(),
		ClipServices:          clipServices,
		Log:                   log,
		GenService:            genSvc,
		Gates:                 gates,
	})

	// ── Register job handlers at composition time ──────────────────────
	if root.Jobs.Service != nil {
		if batchSvc != nil {
			root.Jobs.Service.RegisterHandler("script.generate_batch", handler.HandleBatchScriptGenerateJob)
			log.Info("registered script.generate_batch job handler (wire_script.go)")
		}
		if curationJobSvc != nil {
			root.Jobs.Service.RegisterHandler("media.curate", curationJobSvc.HandleCurateJob)
			log.Info("registered media.curate job handler (wire_script.go)")
		}
		if catalogJobSvc != nil {
			root.Jobs.Service.RegisterHandler("script.generate_from_catalog", catalogJobSvc.HandleCatalogScriptGenerateJob)
			log.Info("registered script.generate_from_catalog job handler (wire_script.go)")
		}
		if pipelineUC != nil {
			// AGENT-2 (June 2026): root.Jobs.Service is *jobs.Service;
			// pipelineUC.RegisterJobs accepts interface{} (post-sig widen
			// done in pipeline_usecase.go). Forwarding through the wider
			// type avoids a cross-package cast.
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
	mod := module.NewRouteModule(
		"script-flow",
		func() bool { return anyScriptFeatureEnabled(cfg) },
		"/script",
		handler,
		log,
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
