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
//
// PR 13 (June 2026): unified pipeline construction moved before handler —
// MediaCurator now depends on GenerateOneUseCase, which requires normCfg /
// sourceReg / ppReg to already exist. The handler receives a fully-populated
// mediaCurator instead of nil.

package app

import (
	"context"
	"fmt"
	"strings"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"

	artlistpkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

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

	// ── Media curator (deferred: needs oneUC) ────────────────────────
	// PR 13 (June 2026): mediaCurator is constructed after the unified
	// pipeline is wired (normCfg, sourceReg, ppReg, oneUC) so it can
	// receive *GenerateOneUseCase instead of the now-removed *Engine.
	var mediaCurator *scripts.MediaCurator

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

	// ── Unified generation pipeline (PR8, June 2026) ───────────────────
	// Constructed BEFORE the handler so mediaCurator can consume oneUC.
	//   SourceRegistry (4 resolvers) → PostProcessorRegistry →
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
		MaxBatchWorkers:          cfg.Scripts.MaxBatchWorkers,
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

	// Search resolver: wired via semanticSearchAdapter bridging
	// root.Domains.RealtimeSearch (scripts.RealtimeSearchService)
	// to scripts.SemanticSearchPort. SourceSearch sources now
	// resolve through Qdrant semantic search.
	if root.Domains.RealtimeSearch != nil && clipSourceBuilder != nil {
		searchAdapter := &semanticSearchAdapter{realtime: root.Domains.RealtimeSearch}
		sourceReg.Register(scriptpkg.SourceSearch, scripts.NewSearchSourceResolver(searchAdapter, clipSourceBuilder, log))
	}

	// Curate resolver (PR E, June 2026): extracted from MediaCurator.
	var curateResolver *scripts.CurateSourceResolver
	if clipSourceBuilder != nil {
		curateResolver = scripts.NewCurateSourceResolver(clipSourceBuilder, log)
		sourceReg.Register(scriptpkg.SourceCurate, curateResolver)
	}

	// Wire ClipSearchPort when Qdrant is enabled (PJ-CURATE-1, June 2026).
	// Ollama client serves as the embedder via qdrant.NewTextEmbedderAdapter.
	// Constructed once and shared between CurateSourceResolver and MediaCurator.
	//
	// Compile-time assertion: *ollamaclient.Client satisfies asset.Embedder
	// (structural typing — if the Embed signature ever drifts, this fails).
	var _ asset.Embedder = (*ollamaclient.Client)(nil)

	var clipSearchPort scripts.ClipSearchPort
	if root.Process != nil && root.Process.QdrantSearcher != nil && gen != nil {
		if ollamaClient := gen.GetClient(); ollamaClient != nil {
			embedder := qdrant.NewTextEmbedderAdapter(ollamaClient)
			clipSearchPort = qdrant.NewClipSearchAdapter(root.Process.QdrantSearcher, embedder, "text", log)
			log.Info("ClipSearchPort wired (Qdrant + Ollama embedder)")
		}
	}
	if curateResolver != nil && clipSearchPort != nil {
		curateResolver.SetClipSearchPort(clipSearchPort)
	}

	// PostProcessorRegistry: individually-testable postprocessors
	// registered at composition time and frozen before use.
	// Replaces the monolithic Pipeline.Run.
	ppReg := scripts.NewPostProcessorRegistry(log)

	// Entities + Metadata: deferred — PostGenFunc callback not wired.
	// When a plan requests these, the registry warns "not registered"
	// and skips cleanly. PR 8 wires the callback.

	// Document processor.
	var genDocsSvc *scripts.DocumentsService
	if root.Drive.DocClient != nil {
		genDocsSvc = scripts.NewDocumentsService(root.Drive.DocClient, log, cfg.Drive.ScriptsGenFolder())
		ppReg.Register(scripts.NewDocumentProcessor(genDocsSvc, nil))
		log.Info("wireScriptFlow: Document processor registered")
	} else {
		log.Warn("wireScriptFlow: root.Drive.DocClient is nil — Document processor skipped")
	}

	// Persistence processor (PR 5: now the single persistence owner;
	// engine no longer writes to SQLite. Constructor takes the
	// logger for idempotency-hit / replay diagnostics).
	ppReg.Register(scripts.NewPersistenceProcessor(scriptsRepoAdapter, log))

	// Image processor — adapted from *imgservice.Service to scripts.ImageGenService.
	if root.Domains.ImageService != nil {
		imgAdapter := &imageGenSvcAdapter{svc: root.Domains.ImageService}
		ppReg.Register(scripts.NewImageProcessor(imgAdapter, log))
	}

	// Voiceover processor — adapted from *voiceover.Service to scripts.VoiceoverService.
	if root.Domains.VoiceoverService != nil {
		voAdapter := &voiceoverSvcAdapter{svc: root.Domains.VoiceoverService}
		ppReg.Register(scripts.NewVoiceoverProcessor(voAdapter, log))
	}

	// Freeze the source registry — no more resolvers after composition.
	sourceReg.Freeze()
	ppReg.Freeze()

	// Use cases: one → many → job handler.
	oneUC := scripts.NewGenerateOneUseCase(normCfg, sourceReg, engine, ppReg, log)
	manyUC := scripts.NewGenerateManyUseCase(oneUC, log)

	// ── Media curator (PR 13: uses oneUC) ──────────────────────────
	if root.Repos.ClipsRepo != nil && engine != nil {
		mediaCurator = scripts.NewMediaCurator(cfg.ClipIndexer.ServerURL, root.Repos.ClipsRepo, clipSourceBuilder, log)
		if clipSearchPort != nil {
			mediaCurator.SetClipSearchPort(clipSearchPort)
		}
	}

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

	// ── Construct handler ──────────────────────────────────────────────
	handler := scriptapi.NewScriptFlowHandler(scriptapi.ScriptFlowDeps{
		Engine:        engine,
		Section:       sectionRegen,
		CacheEviction: cacheEvictionUC,
		Image:         root.Domains.ImageService,
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

	// ── Register HTTP module ───────────────────────────────────────────
	mod := module.NewRouteModule(
		"script-flow",
		func() bool { return anyScriptFeatureEnabled(cfg) },
		"/script",
		handler,
		log,
	)
	return tryRegisterModuleStrict(registry, log, mod, WithRegistrationPoint("register.ScriptFlow"))
}

// ── Adapters ────────────────────────────────────────────────────────────────

// imageGenSvcAdapter adapts *imgservice.Service → scripts.ImageGenService.
// The concrete SearchAndDownload takes tags []string; the interface takes
// extra interface{}. We bridge the {extra} → tags conversion.
type imageGenSvcAdapter struct {
	svc interface {
		SearchAndDownload(ctx context.Context, name, description, query, language string, tags []string) (*asset.ImageAsset, error)
	}
}

func (a *imageGenSvcAdapter) SearchAndDownload(ctx context.Context, name, description, query, language string, extra interface{}) (*asset.ImageAsset, error) {
	var tags []string
	if extra != nil {
		if t, ok := extra.([]string); ok {
			tags = t
		}
	}
	if a == nil || a.svc == nil {
		return nil, nil
	}
	return a.svc.SearchAndDownload(ctx, name, description, query, language, tags)
}

func (a *imageGenSvcAdapter) GenerateSmartImage(ctx context.Context, name, description, style string, prompts, tags []string, width, height int, extra string, flag bool) (*asset.ImageAsset, error) {
	return nil, fmt.Errorf("GenerateSmartImage not supported through ImageProcessor")
}

// voiceoverSvcAdapter adapts *voiceover.Service → scripts.VoiceoverService.
// The concrete Generate returns *voiceover.VoiceoverResult; the interface
// returns interface{}. We bridge the return-type conversion.
type voiceoverSvcAdapter struct {
	svc interface {
		Generate(ctx context.Context, text, language, filename string) (*voiceover.VoiceoverResult, error)
	}
}

func (a *voiceoverSvcAdapter) Generate(ctx context.Context, text, language, filename string) (interface{}, error) {
	if a == nil || a.svc == nil {
		return nil, nil
	}
	return a.svc.Generate(ctx, text, language, filename)
}

// semanticSearchAdapter adapts scripts.RealtimeSearchService → scripts.SemanticSearchPort.
type semanticSearchAdapter struct {
	realtime scripts.RealtimeSearchService
}

// SearchByText delegates to RealtimeSearchService.SearchClips.
// source and mediaType are passed as empty strings because
// SourceSpec carries no source/mediaType fields — the search
// resolver matches across all sources and all media types.
// Language is not used by the current realtime implementation.
func (a *semanticSearchAdapter) SearchByText(ctx context.Context, query string, limit int, language string) ([]scripts.SemanticSearchResult, error) {
	if a == nil || a.realtime == nil {
		return nil, nil
	}
	minScore := 0.0
	matches, err := a.realtime.SearchClips(ctx, query, "", "", limit, minScore)
	if err != nil {
		return nil, err
	}
	results := make([]scripts.SemanticSearchResult, 0, len(matches))
	for _, m := range matches {
		results = append(results, scripts.SemanticSearchResult{
			ClipID: m.ID,
			Name:   m.Name,
			Score:  m.Score,
		})
	}
	return results, nil
}

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
