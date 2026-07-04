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
//
// AZIONE 2 (July 2026): source-resolver factory extracted to
// wire_script_resolvers.go; use-case factory + P04 audit wiring +
// fanout broker adapter extracted to wire_script_usecases.go.
// wireScriptFlow is now a pure orchestrator (~100 LOC) that calls
// the two factories and owns ppReg freeze + job registration +
// handler construction + module registration.

package app

import (
	"context"
	"fmt"
	"strings"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"

	artlistpkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"

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
	// Phase 2 activation (June 2026) — ImageService is now OPTIONAL.
	if root.AI == nil || root.AI.ScriptGen == nil || root.Domains == nil {
		return nil
	}

	engine := root.AI.ScriptEngine
	if engine == nil {
		log.Warn("wireScriptFlow: AIBundle services not fully initialized — skipping ScriptFlow")
		return nil
	}

	scriptsRepoAdapter := adapters.NewRepositoryAdapter(root.Repos.ScriptsRepo)

	// ── Step 1: Source resolvers (factory in wire_script_resolvers.go) ──
	normCfg, sourceReg, clipSourceBuilder, clipSearchPort := buildScriptSourceResolvers(cfg, root, log)

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
	ollamaTranslator := root.AI.OllamaTranslator
	clipServices := usecase.ClipServices{
		Logger:          log,
		DriveSvc:        root.Drive.Reader,
		Translator:      ollamaTranslator,
		Translation:     ollamaTranslator,
		TranslationPort: ollamaTranslator,
		ArtlistFolder:   cfg.Drive.ArtlistFolder(),
		MetadataModel:   metaModel,
	}

	// ── Drive folder / document adapters (impl in wire_script_adapters.go) ──
	driveFolderClient := &driveFolderAdapterImpl{admin: root.Drive.Admin}
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

	// ── Step 2: Post-processor registration + freeze ────────────────────
	ppReg := adapters.NewPostProcessorRegistry(log)
	if err := registerScriptPostProcessors(ppReg, root, cfg, log, scriptsRepoAdapter, metaModel); err != nil {
		return fmt.Errorf("wireScriptFlow: %w", err)
	}
	sourceReg.Freeze()
	ppReg.Freeze()
	if err := validateRequiredProcessors(ppReg, requiredProcessorNames); err != nil {
		return fmt.Errorf("wireScriptFlow: %w", err)
	}

	// ── Step 3: Use cases (factory in wire_script_usecases.go) ──────────
	sectionRegen, cacheEvictionUC, oneUC, manyUC, genJobHandler, mediaCurator := buildScriptUseCases(
		cfg, root, scriptsRepoAdapter, normCfg, sourceReg, ppReg, clipSearchPort, clipSourceBuilder, log,
	)

	// ── Step 4: Job registration ───────────────────────────────────────
	if root.Jobs == nil || root.Jobs.Service == nil {
		return fmt.Errorf("wireScriptFlow: jobs broker is required (Issue 7 / P1 fail-fast)")
	}
	if err := wireScriptChildJobAuditP04(root.Jobs.Service, oneUC, manyUC, normCfg, log); err != nil {
		return fmt.Errorf("wireScriptFlow: P0 #4 audit wiring: %w", err)
	}
	if err := genJobHandler.RegisterJobs(root.Jobs.Service); err != nil {
		return fmt.Errorf("wireScriptFlow: register script.generate job handler: %w", err)
	}
	log.Info("registered script.generate job handler (unified pipeline, PR8)")

	if err := validateScriptGenerateWiring(root, log); err != nil {
		return fmt.Errorf("wireScriptFlow: %w", err)
	}

	// ── Set metadata model ─────────────────────────────────────────────
	if root.AI.ScriptGen != nil {
		root.AI.ScriptGen.SetMetadataModel(metaModel)
	}

	// ── Clip searcher ──────────────────────────────────────────────────
	var clipsSearcher scriptapi.ClipSearcher
	if root.Repos.ClipsRepo != nil {
		clipsSearcher = &clipsNameSearchAdapter{repo: root.Repos.ClipsRepo}
	}

	// ── Step 5: Handler construction (Blocco C1-Step 14) ──────────────
	scriptDeps := scriptapi.Dependencies{
		Engine:                engine,
		Section:               sectionRegen,
		CacheEviction:         cacheEvictionUC,
		Image:                 root.Domains.ImageService,
		Realtime:              root.Domains.RealtimeSearch,
		Association:           root.Domains.AssocService,
		Voiceover:             root.Domains.VoiceoverService,
		AssetTree:             root.Search.AssetTreeService,
		ClipSourceBuilder:     clipSourceBuilder,
		MediaCurator:          mediaCurator,
		Harvest:               harvestSvc,
		ScriptsRepo:           scriptsRepoAdapter,
		Jobs:                  root.Jobs.Facade,
		Registry:              appjobs.Compose(),
		ClipsSearcher:         clipsSearcher,
		AdminToken:            adminToken,
		DriveFolderClient:     driveFolderClient,
		DocumentCreator:       documentCreator,
		DriveScriptsGenFolder: cfg.Drive.ScriptsGenFolder(),
		ClipServices:          clipServices,
		EnabledFunc:           func() bool { return anyScriptFeatureEnabled(cfg) },
		ModuleOpts:            nil,
		Logger:                log,
	}
	scriptDescriptor, err := scriptapi.Build(scriptDeps)
	if err != nil {
		return fmt.Errorf("wireScriptFlow: %w", err)
	}
	sd, ok := scriptDescriptor.(*scriptapi.ScriptDescriptor)
	if !ok || sd == nil {
		return fmt.Errorf("wireScriptFlow: script.Build returned unexpected descriptor type %T (want *scriptapi.ScriptDescriptor)", scriptDescriptor)
	}

	// ── Step 6: Register HTTP module ───────────────────────────────────
	return tryRegisterModule(registry, log, sd)
}

// anyScriptFeatureEnabled returns true when at least one script feature flag
// is on. Moved from script_feature_flags.go (Phase 5 consolidation, June 2026).
func anyScriptFeatureEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.Features.ScriptClipsEnabled || cfg.Features.ScriptDocsEnabled || cfg.Features.ImagesEnabled
}

// registerScripts orchestrates the /api/script/* routing surface.
// Moved from registry_script.go (Phase 5 consolidation, June 2026).
// Calls wireScriptFlow for the canonical use-case delegation and
// registerScriptHistory for the script-history route module.
func registerScripts(ctx context.Context, registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot) error {
	if err := wireScriptFlow(ctx, cfg, log, root, registry); err != nil {
		return err
	}
	return registerScriptHistory(registry, log, cfg, root)
}
