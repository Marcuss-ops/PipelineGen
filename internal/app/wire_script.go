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
//
// FASE 2.A PR2 (June 2026): source-resolver adapters + curation-layer
// adapter extracted to wire_script_sources.go +
// wire_script_curation.go. Wire_script.go stays purely orchestration.
//
// FASE 2.A PR3 (June 2026): post-processor registration block extracted
// to wire_script_postprocess.go; infrastructure
// adapter types (driveFolderAdapterImpl, docCreatorImpl) +
// composition validators (validateScriptGenerateWiring,
// validateRequiredProcessors, requiredProcessorNames) extracted to
// wire_script_adapters.go. wireScriptFlow is now a pure-routing
// orchestrator (wiring → use cases → job handler → handler →
// module registration) with no inline post-processor loop.

package app

import (
	"context"
	"fmt"
	"strings"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"

	artlistpkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptdto "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/dto"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jobs"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// wireScriptFlow constructs and registers the ScriptFlow module.
// Returns an error if module registration fails on duplicate-name or
// frozen-registry (Registries-and-SSOT §"Uniqueness" — composition
// fails closed on duplicate module names, propagated up to WireRegistry).
//
// FASE 2.A PR3 (June 2026): after construction of ppReg the
// orchestrator delegates all canonical postprocessor registrations
// (persistence / document / images / voiceover / entities / metadata /
// clip_bindings / stock_association) to
// registerScriptPostProcessors in wire_script_postprocess.go. The
// orchestrator owns ppReg construction + ppReg.Freeze() +
// post-freeze required-processors validation; the registration
// cluster lives in the dedicated helper.
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

	scriptsRepoAdapter := adapters.NewRepositoryAdapter(root.Repos.ScriptsRepo)

	// ── Clip source builder ────────────────────────────────────────────
	var clipSourceBuilder *usecase.ClipSourceBuilder
	if ollamaClient := gen.GetClient(); ollamaClient != nil {
		clipSourceBuilder = usecase.NewClipSourceBuilder(root.Repos.ClipsRepo, ollamaClient, log)
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
	var mediaCurator *scriptdto.MediaCurator

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
	clipServices := usecase.ClipServices{
		Logger:        log,
		DriveSvc:      root.Drive.DriveUploader,
		Translator:    gen,
		ArtlistFolder: artlistFolder,
		MetadataModel: metaModel,
	}

	// ── Drive folder client adapter (impl in wire_script_adapters.go) ─
	driveFolderClient := &driveFolderAdapterImpl{
		uploader: root.Drive.DriveUploader,
	}

	// ── Document creator adapter (impl in wire_script_adapters.go) ───
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
	sectionRegen := usecase.NewSectionRegenerator(
		scriptsRepoAdapter, gen,
		root.Drive.DocClient, cfg, log,
	)
	cacheEvictionUC := usecase.NewCacheEvictionUseCase(
		gen, usecase.NewMemoryCacheAdapter(memorySvc), log,
	)

	// ── Unified generation pipeline (PR8, June 2026; PR3 orchestration) ─
	// Constructed BEFORE the handler so mediaCurator can consume oneUC.
	//   SourceRegistry (5 resolvers) → PostProcessorRegistry →
	//   GenerateOneUseCase → GenerateManyUseCase → GenerateJobHandler
	// Registered for script.generate — replaces the deleted
	// PipelineUseCase.RegisterJobs block removed in PR7.

	// Normalization config: extracted from the platform config so the
	// normalizer has zero import on internal/platform/config.
	normCfg := adapters.NormalizationConfig{
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
	sourceReg := adapters.NewSourceRegistry(log)
	sourceReg.Register(scriptpkg.SourceText, usecase.NewTextSourceResolver())

	if clipSourceBuilder != nil {
		sourceReg.Register(scriptpkg.SourceClips, usecase.NewClipsSourceResolver(clipSourceBuilder, log))
	}

	// Catalog resolver: reuse searchCatalogAdapter (assets_adapters.go)
	// to bridge *catalog.Repository → appsearch.LocalCatalogPort.
	if root.Repos.CatalogRepo != nil && clipSourceBuilder != nil {
		catAdapter := &searchCatalogAdapter{catalog: root.Repos.CatalogRepo}
		sourceReg.Register(scriptpkg.SourceCatalog, usecase.NewCatalogSourceResolver(catAdapter, clipSourceBuilder, log))
	}

	// ── Qdrant embedder (shared by SemanticSearchPort and ClipSearchPort) ──
	var ollamaEmbedder qdrant.TextEmbedder
	if root.Process != nil && root.Process.QdrantSearcher != nil && gen != nil {
		if ollamaClient := gen.GetClient(); ollamaClient != nil {
			ollamaEmbedder = qdrant.NewTextEmbedderAdapter(ollamaClient)
		}
	}

	// Search resolver: wired via Qdrant SemanticSearchPort directly,
	// bypassing the removed realtime package. SourceSearch sources
	// resolve through Qdrant semantic search. The
	// qdrantSemanticSearchPort adapter lives in wire_script_sources.go.
	if root.Process != nil && root.Process.QdrantSearcher != nil && ollamaEmbedder != nil && clipSourceBuilder != nil {
		searchPort := &qdrantSemanticSearchPort{
			searcher:   root.Process.QdrantSearcher,
			embedder:   ollamaEmbedder,
			vectorName: "text",
			log:        log,
		}
		sourceReg.Register(scriptpkg.SourceSearch, usecase.NewSearchSourceResolver(searchPort, clipSourceBuilder, log))
		log.Info("SourceSearch resolver wired (Qdrant + Ollama embedder)")
	}

	// Curate resolver (PR E, June 2026): extracted from MediaCurator.
	var curateResolver *usecase.CurateSourceResolver
	if clipSourceBuilder != nil {
		curateResolver = usecase.NewCurateSourceResolver(clipSourceBuilder, log)
		sourceReg.Register(scriptpkg.SourceCurate, curateResolver)
	}

	// Wire ClipSearchPort when Qdrant is enabled (PJ-CURATE-1, June 2026).
	// Reuses ollamaEmbedder (constructed above for SemanticSearchPort).
	// clipSearchPortAdapter (in wire_script_sources.go) bridges
	// scriptports.ClipSearchPort → usecase.ClipSearchPort with
	// AssetID → ClipID field-mapping.
	var clipSearchPort scriptports.ClipSearchPort
	if root.Process != nil && root.Process.QdrantSearcher != nil && ollamaEmbedder != nil {
		clipSearchPort = qdrant.NewClipSearchAdapter(root.Process.QdrantSearcher, ollamaEmbedder, "text", log)
		log.Info("ClipSearchPort wired (Qdrant + Ollama embedder)")
	}
	if curateResolver != nil && clipSearchPort != nil {
		curateResolver.SetClipSearchPort(&clipSearchPortAdapter{port: clipSearchPort})
	}

	// PostProcessorRegistry: post-processor registrations moved to
	// wire_script_postprocess.go::registerScriptPostProcessors (PR3,
	// June 2026). The orchestrator owns ppReg construction +
	// freeze; the registration cluster lives in the dedicated
	// helper so wireScriptFlow stays a pure-routing shape.
	ppReg := adapters.NewPostProcessorRegistry(log)

	// Register all canonical postprocessors on ppReg (persistence,
	// document, images, voiceover, entities, metadata, clip_bindings,
	// stock_association). On any Register fail-fast error, wrap with
	// the wireScriptFlow: prefix for fail-closed composition.
	if err := registerScriptPostProcessors(ppReg, root, cfg, log, scriptsRepoAdapter, metaModel); err != nil {
		return fmt.Errorf("wireScriptFlow: %w", err)
	}

	// Freeze the source registry — no more resolvers after composition.
	sourceReg.Freeze()
	ppReg.Freeze()

	// PR 2 (June 2026): post-freeze invariant — every canonical
	// ProcessorRequired name MUST be registered. The validator itself
	// moved to wire_script_adapters.go (PR3). Composition fails
	// closed; the operator sees a clear error instead of runtime
	// panics on the first plan that requested the missing processor.
	if err := validateRequiredProcessors(ppReg, requiredProcessorNames); err != nil {
		return fmt.Errorf("wireScriptFlow: %w", err)
	}

	// Use cases: one → many → job handler.
	oneUC := usecase.NewGenerateOneUseCase(normCfg, sourceReg, engine, ppReg, log)
	// fix/voiceover-group-resolver (June 2026): wire the
	// VoiceoverGroupResolver port so the use case resolves
	// VoiceoverGroup → VoiceoverFolderID BEFORE BuildPlan. The
	// adapter wraps the concrete *voiceover.GroupsResolver (built
	// inline here, mirroring the construction in
	// internal/api/script/handler_flow.go and
	// internal/app/module_media.go::WireAssets). When the voiceover
	// root is not configured OR the asset tree service is missing,
	// the resolver stays nil and the use case short-circuits to a
	// no-op — preserving behaviour parity with pre-PR scripts.
	voRootID := strings.TrimSpace(cfg.Drive.VoiceoverFolder())
	if voRootID != "" && root.Search != nil && root.Search.AssetTreeService != nil {
		if gr, grErr := voiceover.NewGroupsResolver(root.Search.AssetTreeService, log); grErr == nil {
			voAdapter := scriptports.NewVoiceoverGroupsAdapter(gr)
			oneUC.SetVoiceoverRouting(voAdapter, voRootID)
			log.Info("wireScriptFlow: voiceover_group → folder_id resolver wired (fix/voiceover-group-resolver)",
				zap.String("voiceover_root", voRootID))
		} else {
			log.Warn("wireScriptFlow: failed to build voiceover groups resolver — voiceover_group routing disabled",
				zap.Error(grErr))
		}
	}
	manyUC := usecase.NewGenerateManyUseCase(oneUC, log)

	// ── Media curator ───────────────────────────────────────────────
	if root.Repos.ClipsRepo != nil && engine != nil {
		mediaCurator = scriptdto.NewMediaCurator(cfg.ClipIndexer.ServerURL, root.Repos.ClipsRepo, clipSourceBuilder, log)
		if clipSearchPort != nil {
			mediaCurator.SetClipSearchPort(clipSearchPort)
		}
	}

	genJobHandler := jobs.NewGenerateJobHandler(oneUC, manyUC, normCfg, log)

	// Issue 7 / P1 (June 2026): fail-fast when broker is missing or
	// the registration fails on script.generate. The previous
	// `if root.Jobs.Service != nil { log.Warn(...) }` shape silently
	// swallowed broker-missing OR registration-errors, letting the
	// server come up without a script.generate handler -- which then
	// surfaced as a runtime "no handler for script.generate" on the
	// first enqueue. Composition fails closed at this gate; the
	// caller (bootstrap.go) aborts startup on non-nil error.
	if root.Jobs == nil || root.Jobs.Service == nil {
		return fmt.Errorf("wireScriptFlow: jobs broker is required for script.generate registration (Issue 7 / P1 fail-fast)")
	}
	if err := genJobHandler.RegisterJobs(root.Jobs.Service); err != nil {
		return fmt.Errorf("wireScriptFlow: register script.generate job handler: %w", err)
	}
	log.Info("registered script.generate job handler (unified pipeline, PR8)")

	// Issue 7 / P1 (June 2026): post-registration fail-fast on the
	// 3 wiring invariants. The validator moved to
	// wire_script_adapters.go (PR3); the orchestrator keeps the
	// call site.
	if err := validateScriptGenerateWiring(root, log); err != nil {
		return fmt.Errorf("wireScriptFlow: %w", err)
	}

	// ── Set metadata model ─────────────────────────────────────────────
	if gen != nil {
		gen.SetMetadataModel(metaModel)
	}

	// PR-FIX (June 2026): lightweight clip-name searcher for
	// GET /script/clips/search?q= discovery endpoint. The
	// clipsNameSearchAdapter impl lives in wire_script_sources.go.
	var clipsSearcher scriptapi.ClipSearcher
	if root.Repos.ClipsRepo != nil {
		clipsSearcher = &clipsNameSearchAdapter{repo: root.Repos.ClipsRepo}
	}

	// ── Construct handler ──────────────────────────────────────────────
	handler := scriptapi.NewScriptFlowHandler(scriptapi.ScriptFlowDeps{
		Engine:              engine,
		Section:             sectionRegen,
		CacheEviction:       cacheEvictionUC,
		Image:               root.Domains.ImageService,
		Realtime:            root.Domains.RealtimeSearch,
		Association:         root.Domains.AssocService,
		Voiceover:           root.Domains.VoiceoverService,
		AssetTree:           root.Search.AssetTreeService,
		ClipSourceBuilder:   clipSourceBuilder,
		MediaCurator:        mediaCurator,
		Harvest:             harvestSvc,
		ScriptsRepo:         scriptsRepoAdapter,
		Memory:              memorySvc,
		Jobs:                root.Jobs.Facade,
		Registry:            appjobs.Compose(),
		AdminToken:            adminToken,
		DriveFolderClient:     driveFolderClient,
		DocumentCreator:       documentCreator,
		DriveScriptsGenFolder: cfg.Drive.ScriptsGenFolder(),
		ClipServices:          clipServices,
		ClipsSearcher:         clipsSearcher,
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
	return tryRegisterModule(registry, log, mod)
}
