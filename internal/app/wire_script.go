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
	sectionRegen := usecase.NewSectionRegenerator(
		scriptsRepoAdapter, gen,
		root.Drive.DocClient, cfg, log,
	)
	cacheEvictionUC := usecase.NewCacheEvictionUseCase(
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
	sourceReg.Register(scriptpkg.SourceText, adapters.NewTextSourceResolver())

	if clipSourceBuilder != nil {
		sourceReg.Register(scriptpkg.SourceClips, adapters.NewClipsSourceResolver(clipSourceBuilder, log))
	}

	// Catalog resolver: reuse searchCatalogAdapter (assets_adapters.go)
	// to bridge *catalog.Repository → appsearch.LocalCatalogPort.
	if root.Repos.CatalogRepo != nil && clipSourceBuilder != nil {
		catAdapter := &searchCatalogAdapter{catalog: root.Repos.CatalogRepo}
		sourceReg.Register(scriptpkg.SourceCatalog, adapters.NewCatalogSourceResolver(catAdapter, clipSourceBuilder, log))
	}

	// Search resolver: wired via semanticSearchAdapter bridging
	// root.Domains.RealtimeSearch (usecase.RealtimeSearchService)
	// to scriptports.SemanticSearchPort. SourceSearch sources now
	// resolve through Qdrant semantic search.
	if root.Domains.RealtimeSearch != nil && clipSourceBuilder != nil {
		searchAdapter := &semanticSearchAdapter{realtime: root.Domains.RealtimeSearch}
		sourceReg.Register(scriptpkg.SourceSearch, adapters.NewSearchSourceResolver(searchAdapter, clipSourceBuilder, log))
	}

	// Curate resolver (PR E, June 2026): extracted from MediaCurator.
	var curateResolver *adapters.CurateSourceResolver
	if clipSourceBuilder != nil {
		curateResolver = adapters.NewCurateSourceResolver(clipSourceBuilder, log)
		sourceReg.Register(scriptpkg.SourceCurate, curateResolver)
	}

	// Wire ClipSearchPort when Qdrant is enabled (PJ-CURATE-1, June 2026).
	// Ollama client serves as the embedder via qdrant.NewTextEmbedderAdapter.
	// Constructed once and shared between CurateSourceResolver and MediaCurator.
	var clipSearchPort scriptports.ClipSearchPort
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
	ppReg := adapters.NewPostProcessorRegistry(log)

	// Entities + Metadata: deferred — PostGenFunc callback not wired.
	// When a plan requests these, the registry warns "not registered"
	// and skips cleanly. PR 8 wires the callback.

	// Document processor.
	var genDocsSvc *usecase.DocumentsService
	if root.Drive.DocClient != nil {
		genDocsSvc = usecase.NewDocumentsService(root.Drive.DocClient, log, cfg.Drive.ScriptsGenFolder())
		if !ppReg.Register(adapters.NewDocumentProcessor(genDocsSvc, nil)) {
			return fmt.Errorf("wireScriptFlow: failed to register document processor (composition bug)")
		}
	}

	// Persistence processor (PR 5: now the single persistence owner;
	// engine no longer writes to SQLite. Constructor takes the
	// logger for idempotency-hit / replay diagnostics).
	if !ppReg.Register(adapters.NewPersistenceProcessor(scriptsRepoAdapter, log)) {
		return fmt.Errorf("wireScriptFlow: failed to register persistence processor (composition bug or duplicate name)")
	}

	// Image processor — adapted from *imgservice.Service to usecase.ImageGenService.
	if root.Domains.ImageService != nil {
		imgAdapter := &imageGenSvcAdapter{svc: root.Domains.ImageService}
		if !ppReg.Register(adapters.NewImageProcessor(imgAdapter, log)) {
			return fmt.Errorf("wireScriptFlow: failed to register image processor")
		}
	}

	// Voiceover processor — adapted from *voiceover.Service to usecase.VoiceoverService.
	if root.Domains.VoiceoverService != nil {
		voAdapter := &voiceoverSvcAdapter{svc: root.Domains.VoiceoverService}
		if !ppReg.Register(adapters.NewVoiceoverProcessor(voAdapter, log)) {
			return fmt.Errorf("wireScriptFlow: failed to register voiceover processor")
		}
	}

	// PR 3 (June 2026): Entities + Metadata processors, now both
	// ProcessorRequired per the spec. Adapters are nil-tolerant at
	// runtime (graceful-degradation) and the runtime preflight will
	// fail-fast when a plan requests these processors without a
	// real service wired through the composition root. The
	// composition-time validateRequiredProcessors call below
	// confirms both names are registered; the runtime preflight
	// confirms they succeed for any plan that requests them.
	//
	// Both adapters take nil-tolerant interfaces, so composition
	// succeeds even when no real backend is wired in a test
	// fixture. Production deploys should provide a real
	// EntityScriptExtractor and ollama.Generator via root points
	// (future PR; tracked separately).
	entityAdapter := adapters.NewEntityExtractionAdapter(nil)
	if !ppReg.Register(adapters.NewEntitiesProcessor(entityAdapter)) {
		return fmt.Errorf("wireScriptFlow: failed to register entities processor")
	}
	metadataAdapter := adapters.NewMetadataGenerationAdapter(nil, metaModel)
	if !ppReg.Register(adapters.NewMetadataProcessor(metadataAdapter)) {
		return fmt.Errorf("wireScriptFlow: failed to register metadata processor")
	}

	// Freeze the source registry — no more resolvers after composition.
	sourceReg.Freeze()
	ppReg.Freeze()

	// PR 2 (June 2026): post-freeze invariant — every canonical
	// ProcessorRequired name MUST be registered. Names that the
	// composition attempted to register but couldn't (because the
	// dependency was nil, e.g. DocClient missing) are caught here.
	// Composition fails closed; the operator sees a clear error
	// instead of runtime panics on the first plan that requested
	// the missing processor.
	if err := validateRequiredProcessors(ppReg, requiredProcessorNames); err != nil {
		return fmt.Errorf("wireScriptFlow: %w", err)
	}

	// Use cases: one → many → job handler.
	oneUC := usecase.NewGenerateOneUseCase(normCfg, sourceReg, engine, ppReg, log)
	manyUC := usecase.NewGenerateManyUseCase(oneUC, log)

	// ── Media curator ───────────────────────────────────────────────
	if root.Repos.ClipsRepo != nil && engine != nil {
		mediaCurator = scriptdto.NewMediaCurator(cfg.ClipIndexer.ServerURL, root.Repos.ClipsRepo, clipSourceBuilder, log)
		if clipSearchPort != nil {
			mediaCurator.SetClipSearchPort(clipSearchPort)
		}
	}

	genJobHandler := usecase.NewGenerateJobHandler(oneUC, manyUC, normCfg, log)
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
		// typed ports — `usecase.RealtimeSearchService` and
		// `usecase.AssocSearchService`. DomainBundle.RealtimeSearch +
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
	return tryRegisterModule(registry, log, mod)
}

// ── Composition validation: required processors MUST register ────────

// requiredProcessorNames is the canonical list of postprocessor names
// that MUST be registered for a script pipeline to be considered
// production-ready. Composition aborts if any name below is missing.
//
// PR 2 (June 2026): the list mirrors the static ProcessorRequired
// classification declared by each concrete processor. Persistence
// is the single owner of script-table writes (PR 5); Document is
// the canonical doc-creation deliverable. Images / Voiceover /
// Entities / Metadata are ProcessorBestEffort (spec: "configurabile"
// or "best_effort or required based on payload") and not part of
// this list — if they are present at runtime, Run warns; if they
// are absent at runtime, Run warns. Either way, composition does
// NOT fail on them.
var requiredProcessorNames = []string{
	"persistence",
	"document",
	// PR 3 (June 2026): Entities and Metadata are now
	// ProcessorRequired per the user spec. The canonical
	// Composition-time validator fails closed if they are
	// not registered; the runtime preflight fails closed if
	// a plan requests them and the registry has no adapter.
	"entities",
	"metadata",
}

// validateRequiredProcessors checks the post-freeze registry for
// every required processor name. Composition fails-closed: if any
// required name is missing, returns a typed error so the operator
// sees a clear restart-required message instead of silent runtime
// panics on the first plan that requested the missing processor.
//
// Returns a *scriptpkg.PlanInvalidError when one or more required
// processors are missing from the registry. Caller is the
// composition root, which wraps this with a context string.
//
// PR 2 (June 2026): gate that closes the "non-canonical WriteScript
// to dragnet" gap left by the previous partial-registration pattern
// (where composition would silently skip a Register call when the
// underlying dep was nil, then runtime would silently skip the
// postprocessor — leaving the script row unwritten).
func validateRequiredProcessors(ppReg *adapters.PostProcessorRegistry, required []string) *scriptpkg.PlanInvalidError {
	if ppReg == nil {
		return &scriptpkg.PlanInvalidError{
			ItemID:  "wireScriptFlow",
			Details: []string{"preflight: postprocessor registry is nil"},
		}
	}
	if !ppReg.IsFrozen() {
		return &scriptpkg.PlanInvalidError{
			ItemID:  "wireScriptFlow",
			Details: []string{"preflight: postprocessor registry must be frozen before required-processors validation"},
		}
	}
	var missing []string
	for _, name := range required {
		if !ppReg.Registered(name) {
			missing = append(missing, name)
		} else if ppReg.LookupPolicy(name) != adapters.ProcessorRequired {
			// Defensive: composition-side invariant. A name in the
			// required list MUST have the ProcessorRequired
			// classification. If a future PR flips a processor's
			// policy to BestEffort, this check surfaces the
			// dependency drift loudly — the operator MUST update
			// requiredProcessorNames to match.
			missing = append(missing, name+" (registered with non-required policy)")
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &scriptpkg.PlanInvalidError{
		ItemID:  "wireScriptFlow",
		Details: []string{"preflight: required postprocessor(s) not registered at composition: " + strings.Join(missing, ", ")},
	}
}

// ── Adapters ────────────────────────────────────────────────────────────────

// imageGenSvcAdapter adapts *imgservice.Service → usecase.ImageGenService.
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
// The concrete Generate/GenerateWithDestination return *voiceover.VoiceoverResult;
// the interface returns interface{}. We bridge the return-type conversion.
type voiceoverSvcAdapter struct {
	svc interface {
		Generate(ctx context.Context, text, language, filename string) (*voiceover.VoiceoverResult, error)
		GenerateWithDestination(ctx context.Context, text, language, filename string, dest *voiceover.DestinationRequest) (*voiceover.VoiceoverResult, error)
	}
}

func (a *voiceoverSvcAdapter) Generate(ctx context.Context, text, language, filename string) (interface{}, error) {
	if a == nil || a.svc == nil {
		return nil, nil
	}
	return a.svc.Generate(ctx, text, language, filename)
}

func (a *voiceoverSvcAdapter) GenerateWithDestination(ctx context.Context, text, language, filename string, dest *voiceover.DestinationRequest) (interface{}, error) {
	if a == nil || a.svc == nil {
		return nil, nil
	}
	return a.svc.GenerateWithDestination(ctx, text, language, filename, dest)
}

// semanticSearchAdapter adapts scripts.RealtimeSearchService → scripts.SemanticSearchPort.
type semanticSearchAdapter struct {
	realtime usecase.RealtimeSearchService
}

// SearchByText delegates to RealtimeSearchService.SearchClips.
// source and mediaType are passed as empty strings because
// SourceSpec carries no source/mediaType fields — the search
// resolver matches across all sources and all media types.
// Language is not used by the current realtime implementation.
func (a *semanticSearchAdapter) SearchByText(ctx context.Context, query string, limit int, language string) ([]scriptports.SemanticSearchResult, error) {
	if a == nil || a.realtime == nil {
		return nil, nil
	}
	minScore := 0.0
	matches, err := a.realtime.SearchClips(ctx, query, "", "", limit, minScore)
	if err != nil {
		return nil, err
	}
	results := make([]scriptports.SemanticSearchResult, 0, len(matches))
	for _, m := range matches {
		results = append(results, scriptports.SemanticSearchResult{
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
	docsSvc := usecase.NewDocumentsService(d.docClient, d.log, d.driveFolderID)
	resolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
		return input, nil // raw ID assumed (caller resolved beforehand)
	}
	return docsSvc.CreateDoc(ctx, title, content, resolveFolder, folderID)
}
