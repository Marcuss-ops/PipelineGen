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
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptdto "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/dto"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jobs"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"

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
		gen, usecase.NewMemoryCacheAdapter(memorySvc), log,
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

	// Search resolver: wired via semanticSearchAdapter bridging
	// root.Domains.RealtimeSearch (usecase.RealtimeSearchService)
	// to scriptports.SemanticSearchPort. SourceSearch sources now
	// resolve through Qdrant semantic search.
	if root.Domains.RealtimeSearch != nil && clipSourceBuilder != nil {
		searchAdapter := &semanticSearchAdapter{realtime: root.Domains.RealtimeSearch}
		sourceReg.Register(scriptpkg.SourceSearch, usecase.NewSearchSourceResolver(searchAdapter, clipSourceBuilder, log))
	}

	// Curate resolver (PR E, June 2026): extracted from MediaCurator.
	var curateResolver *usecase.CurateSourceResolver
	if clipSourceBuilder != nil {
		curateResolver = usecase.NewCurateSourceResolver(clipSourceBuilder, log)
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
		// clipSearchPort is the canonical ports.ClipSearchPort built by
		// qdrant.NewClipSearchAdapter (returns []ports.ClipSearchHit).
		// curateResolver.SetClipSearchPort wants the typed usecase.ClipSearchPort
		// whose SearchClips returns []scriptpkg.SearchResultItem. The
		// struct-field bridge at the bottom of this file maps
		// AssetID → ClipID + threading Name/Score/Source. Without this
		// adapter the curate resolver sees a wrong-typed port and the
		// build breaks with the "wrong type for method SearchClips"
		// mismatch.
		curateResolver.SetClipSearchPort(&clipSearchPortAdapter{port: clipSearchPort})
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

	// PR 7 (June 2026): register ClipBindingsProcessor so the
	// postprocessor walk produces ONE canonical set of scene-clip
	// bindings consumed by both the Google Doc builder (via
	// DocumentProcessor) AND the JSON response writer (via
	// result.Output.SpecScene.Scenes). BestEffort policy means a
	// missing-registered observation is a warning, not a hard
	// fail; the processor is a no-op when plan.ClipEvidence is
	// nil/empty so text-only paths are unaffected. The previous
	// pre-PR-7 registration was dropped because the processor's
	// signature `(ctx, plan, model, *PostProcessArtifact)` drifted
	// from the canonical PostProcessor interface and could not
	// satisfy `ppReg.Register`. The new signature is
	// `(ctx, plan, input ProcessInput) (*PostProcessResult, error)`
	// and the processor is the canonical single-owned binding assigner.
	if !ppReg.Register(adapters.NewClipBindingsProcessor(log)) {
		return fmt.Errorf("wireScriptFlow: failed to register clip_bindings processor (composition bug or duplicate name)")
	}

	// Stock association processor — wraps Qdrant searcher for
	// per-scene vector search over stock-indexed assets. BestEffort
	// policy: a missing or failing stock search does not block the
	// pipeline. Falls back to the scene's Clip.DriveLink when no
	// stock match is found.
	if root.Process != nil && root.Process.QdrantSearcher != nil && gen != nil {
		if ollamaClient := gen.GetClient(); ollamaClient != nil {
			embedder := qdrant.NewTextEmbedderAdapter(ollamaClient)
			stockSearchPort := qdrant.NewStockSearchAdapter(root.Process.QdrantSearcher, embedder, "text", log)
			if !ppReg.Register(adapters.NewStockAssociationProcessor(stockSearchPort, log)) {
				return fmt.Errorf("wireScriptFlow: failed to register stock_association processor (composition bug or duplicate name)")
			}
			log.Info("StockAssociationProcessor wired (Qdrant + Ollama embedder)")
		}
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
		// Issue 4 (June 2026, P1): wire the canonical job-type Registry
		// so EnqueueGenerationJob sources MaxRetries from
		// registry.DefaultMaxRetries(script.generate) instead of the
		// pre-Issue-4 hard-coded 3-retry fallback.
		Registry:              appjobs.Compose(),
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

// SearchAndDownload bridges the concrete *imgservice.Service signature
// (returns *asset.ImageAsset) to the canonical adapters.ImageGenService
// interface (returns *adapters.ImageResult). ImageResult exposes only
// SourceURL (see adapters/processor_images.go:31), so the bridge
// copies that single field after a defensive nil-check on the
// underlying asset.ImageAsset. A nil inner result becomes an EMPTY
// ImageResult (SourceURL="") so the downstream ImageProcessor gets a
// typed non-nil pointer — matching the existing processor code path
// in processor_images.go::Process where `asset != nil { url = asset.SourceURL }`.
//
// TODO #8 (drift-fix PR, June 2026): the previous `(ctx, …) (*asset.ImageAsset, error)`
// return shape satisfies the wrong interface — *adapters.ImageResult
// is the canonical typed result for ImageGenService downstream of
// the line-248 NewImageProcessor call. The bridge here is the
// contained seam: any future schema drift on either side is caught
// at this single method.
func (a *imageGenSvcAdapter) SearchAndDownload(ctx context.Context, name, description, query, language string, extra interface{}) (*adapters.ImageResult, error) {
	var tags []string
	if extra != nil {
		if t, ok := extra.([]string); ok {
			tags = t
		}
	}
	if a == nil || a.svc == nil {
		return nil, nil
	}
	imgAsset, err := a.svc.SearchAndDownload(ctx, name, description, query, language, tags)
	if err != nil {
		return nil, err
	}
	if imgAsset == nil {
		return &adapters.ImageResult{}, nil
	}
	return &adapters.ImageResult{SourceURL: imgAsset.SourceURL}, nil
}

func (a *imageGenSvcAdapter) GenerateSmartImage(ctx context.Context, name, description, style string, prompts, tags []string, width, height int, extra string, flag bool) (*asset.ImageAsset, error) {
	return nil, fmt.Errorf("GenerateSmartImage not supported through ImageProcessor")
}

// voiceoverSvcAdapter adapts *voiceover.Service → scripts.VoiceoverService.
// The wrapped concrete *voiceover.Service already exposes the canonical
// typed return *voiceover.VoiceoverResult; this adapter is a thin
// nil-tolerant seam that satisfies the scripts.VoiceoverService port
// (preserving the wire-up registration at adapter construction time).
//
// PR-VOICEOVER-STREAM-SUPERSESSION-2026-06-28 / Step 7 M2 (June 2026):
// returns are now typed (*voiceover.VoiceoverResult, error) matching the
// canonical wire shape — no more interface{} magic-string passthrough.
// extractVoiceoverPaths is gone (processor_voiceover.go); the typed result
// reaches processor_voiceover.go::Process unboxed via direct struct
// field reads (result.Path, result.DriveLink).
type voiceoverSvcAdapter struct {
	svc interface {
		Generate(ctx context.Context, text, language, filename string) (*voiceover.VoiceoverResult, error)
		GenerateWithDestination(ctx context.Context, text, language, filename string, dest *voiceover.DestinationRequest) (*voiceover.VoiceoverResult, error)
	}
}

func (a *voiceoverSvcAdapter) Generate(ctx context.Context, text, language, filename string) (*voiceover.VoiceoverResult, error) {
	if a == nil || a.svc == nil {
		return nil, nil
	}
	return a.svc.Generate(ctx, text, language, filename)
}

func (a *voiceoverSvcAdapter) GenerateWithDestination(ctx context.Context, text, language, filename string, dest *voiceover.DestinationRequest) (*voiceover.VoiceoverResult, error) {
	if a == nil || a.svc == nil {
		return nil, nil
	}
	return a.svc.GenerateWithDestination(ctx, text, language, filename, dest)
}

// semanticSearchAdapter adapts scripts.RealtimeSearchService → scripts.SemanticSearchPort.
type	semanticSearchAdapter struct {
		realtime usecase.RealtimeSearchService
	}

	// SearchByText delegates to RealtimeSearchService.SearchClips.
	func (a *semanticSearchAdapter) SearchByText(ctx context.Context, query string, limit int, language string) ([]usecase.SemanticSearchResult, error) {
		if a == nil || a.realtime == nil {
			return nil, nil
		}
		minScore := 0.0
		matches, err := a.realtime.SearchClips(ctx, query, "", "", limit, minScore)
		if err != nil {
			return nil, err
		}
		results := make([]usecase.SemanticSearchResult, 0, len(matches))
		for _, m := range matches {
			results = append(results, usecase.SemanticSearchResult{
				ClipID: m.ID,
				Name:   m.Name,
				Score:  m.Score,
			})
		}
		return results, nil
	}

// ── ClipSearchPort type bridge (TODO #8 drift-fix, June 2026) ──────────
//
// The qdrant-backed `scriptports.ClipSearchPort` produced by
// qdrant.NewClipSearchAdapter (used by clipSearchPort above) returns
// `[]ports.ClipSearchHit`. The typed `usecase.ClipSearchPort` expected
// by `curateResolver.SetClipSearchPort` (defined in
// source_resolver_curate.go:50) returns `[]scriptpkg.SearchResultItem`.
// Field mapping is: AssetID → ClipID (the only rename needed); Name,
// Score, Source are 1:1; DriveLink has no source field so it's left
// empty. The bridge lives here (composition root, not the qdrant
// adapter) so the canonical infra package stays oblivious to the
// usecase-typed SearchResultItem.
//
// Scope rationale (AGENTS.md Pattern 0): a single sealed bridge struct
// at the seam is preferred over a typed port everywhere because only
// TWO consumers exist (curateResolver + mediaCurator) and
// mediaCurator accepts interface{} — so the search-rules interface{}
// carrier already used by scriptdto.MediaCurator avoids second-bridge
// copies.

type clipSearchPortAdapter struct {
	port scriptports.ClipSearchPort
}

func (a *clipSearchPortAdapter) SearchClips(ctx context.Context, q scriptports.ClipSearchQuery) ([]scriptpkg.SearchResultItem, error) {
	if a == nil || a.port == nil {
		return nil, nil
	}
	hits, err := a.port.SearchClips(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return []scriptpkg.SearchResultItem{}, nil
	}
	out := make([]scriptpkg.SearchResultItem, 0, len(hits))
	for _, h := range hits {
		out = append(out, scriptpkg.SearchResultItem{
			ClipID: h.AssetID,
			Name:   h.Name,
			Score:  h.Score,
			Source: h.Source,
		})
	}
	return out, nil
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
