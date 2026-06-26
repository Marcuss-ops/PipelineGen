// Package app — wire_script.go canonicalises the ScriptFlow module wiring
// outside of the monolithic registry.go.
//
// Agente 4 — H (June 2026): extracted from registry.go. ClipServices is
// pre-built here (access to concrete infrastructure) and passed to the
// handler. Job registration happens at composition time.
//
// Registries-and-SSOT (June 2026): function now returns error so the
// module registration at the bottom propagates duplicate-name /
// frozen-registry failures up to WireRegistry. Every early-return
// returns nil; only the final Register call returns tryRegisterModule's
// possible error.
//
// PR7 (June 2026): removed legacy job registrations (BatchJobHandler,
// CatalogJobServiceImpl, PipelineUseCase.RegisterJobs), GenerationService,
// GenerateBatchUseCase, FeatureGates, PipelineUseCase construction.
//
// PR8 (June 2026): wired unified generation pipeline — SourceRegistry
// (4 resolvers), Pipeline (post-generation), GenerateOneUseCase,
// GenerateManyUseCase, GenerateJobHandler registered for
// script.generate. Replaces the deleted PipelineUseCase block.

package app

import (
	"context"
	"strings"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"

	artlistpkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"go.uber.org/zap"
)

// wireScriptFlow constructs and registers the ScriptFlow module.
// Returns an error if module registration fails on duplicate-name or
// frozen-registry (Registries-and-SSOT §"Uniqueness" — composition
// fails closed on duplicate module names, propagated up to WireRegistry).
func wireScriptFlow(ctx context.Context, cfg *config.Config, log *zap.Logger, root *ComposeRoot, registry *module.Registry) error {
	// Phase 2 activation (June 2026) — ImageService is now OPTIONAL:
	// text-only script generation no longer requires ImageService to be
	// wired. The gate that previously aborted whole ScriptFlow when
	// ImageService was missing prevented operators from running a
	// bare script generation request.
	if root.AI == nil || root.AI.ScriptGen == nil || root.Domains == nil {
		return nil
	}

	memorySvc := root.AI.MemoryService
	engine := root.AI.ScriptEngine
	gen := root.AI.ScriptGen

	if memorySvc == nil || engine == nil {
		log.Warn("wireScriptFlow: AIBundle services not fully initialized — skipping ScriptFlow")
		return nil
	}

	scriptsRepoAdapter := scripts.NewRepositoryAdapter(root.Repos.ScriptsRepo)

	// ── Clip source builder ────────────────────────────────────────────
	var clipSourceBuilder *scripts.ClipSourceBuilder
	if ollamaClient := gen.GetClient(); ollamaClient != nil {
		clipSourceBuilder = scripts.NewClipSourceBuilder(root.Repos.ClipsRepo, ollamaClient, log)
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
	if root.Repos.ClipsRepo != nil && engine != nil {
		mediaCurator = scripts.NewMediaCurator(cfg.ClipIndexer.ServerURL, root.Repos.ClipsRepo, clipSourceBuilder, engine, log)
	}

	// ── Harvest service ────────────────────────────────────────────────
	var _ = artlistpkg.LoadPresets
	var _ = cfg.Drive.ArtlistFolder()
	var harvestSvc scriptapi.AutoHarvestService
	harvestSvc = nil // clipresolver.NewJobHarvestService removed (commit d61068b3)

	// ── Pre-built ClipServices (avoids infrastructure imports in api/script) ──
	metaModel := strings.TrimSpace(cfg.External.OllamaModel)
	if mm := strings.TrimSpace(cfg.External.OllamaMetadataModel); mm != "" {
		metaModel = mm
	}
	artlistFolder := cfg.Drive.ArtlistFolder()
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

	// ── Curation job service ───────────────────────────────────────────
	var curationJobSvc *scripts.CurationJobServiceImpl
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

	// ── Construct handler ──────────────────────────────────────────────
	handler := scriptapi.NewScriptFlowHandler(scriptapi.ScriptFlowDeps{
		Engine:                engine,
		Section:               sectionRegen,
		CacheEviction:         cacheEvictionUC,
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
	})

	// ── Register job handlers at composition time ──────────────────────
	if root.Jobs.Service != nil {
		if curationJobSvc != nil {
			root.Jobs.Service.RegisterHandler(job.TypeMediaCurate, curationJobSvc.HandleCurateJob)
			log.Info("registered media.curate job handler (wire_script.go)")
		}
	}

	// ── Unified generation pipeline (PR8, June 2026) ───────────────────
	// Constructs the canonical generation stack:
	//   SourceRegistry (4 resolvers) → Pipeline (post-gen) →
	//   GenerateOneUseCase → GenerateManyUseCase → GenerateJobHandler
	// Registered for script.generate — replaces the deleted
	// PipelineUseCase.RegisterJobs block removed in PR7.

	// Normalization config: extracted from the platform config so the
	// normalizer has zero import on internal/platform/config.
	normCfg := scripts.NormalizationConfig{
		DefaultLanguage:          cfg.Scripts.DefaultLanguage,
		DefaultTone:              cfg.Scripts.DefaultTone,
		DefaultDurationSeconds:   cfg.Scripts.DefaultDurationSeconds,
		OllamaModel:              cfg.External.OllamaModel,
		ChannelID:                cfg.Scripts.ChannelID,
		MinWordFloor:             cfg.Scripts.MinWordFloor,
		PromptVersion:            "v1",
		EditorPromptVersion:      "v1",
		QAPromptVersion:          "v1",
		DefaultSentencesPerImage: 10,
		DefaultImagesPerScene:    2,
	}

	// Source registry: one resolver per source type.
	sourceReg := scripts.NewSourceRegistry(log)
	sourceReg.Register(scriptpkg.SourceText, scripts.NewTextSourceResolver())

	if clipSourceBuilder != nil {
		sourceReg.Register(scriptpkg.SourceClips, scripts.NewClipsSourceResolver(clipSourceBuilder, log))
	}

	// Catalog resolver: reuse searchCatalogAdapter (assets_adapters.go)
	// to bridge *catalog.Repository → appsearch.LocalCatalogPort.
	if root.Repos.CatalogRepo != nil && clipSourceBuilder != nil {
		catAdapter := &searchCatalogAdapter{catalog: root.Repos.CatalogRepo}
		sourceReg.Register(scriptpkg.SourceCatalog, scripts.NewCatalogSourceResolver(catAdapter, clipSourceBuilder, log))
	}

	// Search resolver: deferred — requires qdrant.Searcher +
	// qdrant.TextEmbedder which are not yet accessible from
	// ProcessBundle. SourceSearch gracefully falls back to
	// SourceResolutionError at runtime.

	// Pipeline: post-generation phases (entities, metadata, doc).
	// PostGenFunc is nil — entity extraction + metadata deferred
	// to future postprocessor implementations.
	// ScenesService is nil — scene image generation deferred.
	var genDocsSvc *scripts.DocumentsService
	if root.Drive.DocClient != nil {
		genDocsSvc = scripts.NewDocumentsService(root.Drive.DocClient, log, cfg.Drive.ScriptsGenFolder())
	}
	pipeline := scripts.NewPipeline(log, "unified", nil, genDocsSvc, nil, nil)

	// Use cases: one → many → job handler.
	oneUC := scripts.NewGenerateOneUseCase(normCfg, sourceReg, engine, pipeline, log)
	manyUC := scripts.NewGenerateManyUseCase(oneUC, log)

	genJobHandler := scripts.NewGenerateJobHandler(oneUC, manyUC, normCfg, log)
	if root.Jobs.Service != nil {
		if err := genJobHandler.RegisterJobs(root.Jobs.Service); err != nil {
			log.Warn("wireScriptFlow: failed to register script.generate job handler", zap.Error(err))
		} else {
			log.Info("registered script.generate job handler (unified pipeline, PR8)")
		}
	}

	// ── Set metadata model ─────────────────────────────────────────────
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
	return tryRegisterModule(registry, log, mod)
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
	resolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
		return input, nil // raw ID assumed (caller resolved beforehand)
	}
	return docsSvc.CreateDoc(ctx, title, content, resolveFolder, folderID)
}
