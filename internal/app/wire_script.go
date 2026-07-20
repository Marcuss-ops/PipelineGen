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
//
// Commit 1 P0 #4 audit (July 2026): extracted wireScriptFanout
// and scriptItemFanoutBrokerAdapter to package level (Go does not allow
// nested functions). Moved root.Jobs nil check before dereference. Fixed
// EnqueueScriptItem: ret.JobID→ret.ID, typed ScriptGenerateItemPayload
// instead of double-marshalling, constant reference fix.
//
// PR-script-deps-slim (July 2026, P1): slim form of Dependencies
// (2 small dep bags + ClipsSearcher + AdminToken + 3 build-time
// fields, was 22+3 fields with 12 ignored). The 12 ignored
// ScriptFlowDeps fields (Engine, Section, CacheEviction, Image,
// Realtime, Association, Voiceover, AssetTree, ClipSourceBuilder,
// MediaCurator, Harvest, ScriptsRepo, DriveScriptsGenFolder,
// ClipServices) are RETIRED. The corresponding local-variable
// construction in wireScriptFlow (engine, harvestSvc, clipServices,
// ollamaTranslator, driveFolderClient, documentCreator) is
// dropped in lockstep. The adapter types (driveFolderAdapterImpl,
// docCreatorImpl) are RETAINED in wire_script_adapters.go for
// the canonical FacadeHandler (future typed-port consumers per
// PR-SCRIPT-FACADE-EXTRACT).

package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	appvideo "github.com/Marcuss-ops/PipelineGen/internal/application/video"

	scriptgenrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scriptgeneration"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/scriptgeneration"

	"go.uber.org/zap"
)

func remotionBaseURL() string {
	if value := strings.TrimSpace(os.Getenv("VELOX_REMOTION_URL")); value != "" {
		return value
	}
	return "http://127.0.0.1:4317"
}

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
//
// PR-script-deps-slim (July 2026, P1): post-slim, the function
// no longer constructs the 12 ignored deps (Engine + Section +
// CacheEviction + Image + Realtime + Association + Voiceover +
// AssetTree + ClipSourceBuilder + MediaCurator + Harvest +
// ScriptsRepo + DriveScriptsGenFolder + ClipServices). The
// slim Dependencies is 6 fields (Generate + Jobs +
// ClipsSearcher + AdminToken + 3 build-time). The 2 routes that
// depended on sectionRegen + cacheEviction (RegenerateSection +
// EvictCache) are RETIRED in lockstep.
func wireScriptFlow(ctx context.Context, cfg *config.Config, log *zap.Logger, root *ComposeRoot, registry *module.Registry) error {
	// Phase 2 activation (June 2026) — root.AI / root.Domains required.
	// PR-SCRIPTCONTRACT-COMPOSITION-WIRE (July 2026): root.Drive added to
	// the canonical guard. Without this guard, a nil Drive bundle would
	// panic with nil-pointer deref instead of failing-closed
	// (godlike/07 fail-fast-at-input).
	if root.AI == nil || root.AI.ScriptGen == nil || root.Domains == nil || root.Drive == nil {
		return nil
	}

	if root.AI.ScriptEngine == nil {
		log.Warn("wireScriptFlow: AIBundle services not fully initialized — skipping ScriptFlow")
		return nil
	}

	// ── Wire ScriptVoiceoverGenerator (P1 verdetto) ─────────────────────
	// Constructs the VoiceoverGenerator adapter from the TTS audio processor
	// when available. Used by the script generation runner's Stage 4
	// (GENERATING_VOICEOVERS).
	if root.Domains.AudioProcessor != nil {
		voPath := cfg.Storage.VoiceoversPath()
		root.AI.ScriptVoiceoverGenerator = NewScriptVoiceoverGenerator(root.Domains.AudioProcessor, voPath, log)
		log.Info("wireScriptFlow: ScriptVoiceoverGenerator wired",
			zap.String("output_dir", voPath))
	} else {
		log.Warn("wireScriptFlow: ScriptVoiceoverGenerator NOT wired (no audio processor) — voiceover stage will be skipped")
	}

	// ── Step 1: Source resolvers (factory in wire_script_resolvers.go) ──
	normCfg, sourceReg, clipSourceBuilder, clipSearchPort := buildScriptSourceResolvers(cfg, root, log)

	// ── Pre-compute metadata model (used by post-processor + AI bundle) ──
	metaModel := strings.TrimSpace(cfg.External.OllamaModel)
	if mm := strings.TrimSpace(cfg.External.OllamaMetadataModel); mm != "" {
		metaModel = mm
	}

	// ── Step 2: Post-processor registration + freeze ────────────────────
	scriptsRepoAdapter := adapters.NewRepositoryAdapter(root.Repos.ScriptsRepo)
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
	// PR-script-deps-slim: sectionRegen + cacheEvictionUC are
	// RETIRED from buildScriptUseCases return tuple (the
	// corresponding RegenerateSection + EvictCache routes were
	// always 503 because the handler fields were never assigned).
	// scriptsRepoAdapter is no longer threaded into the factory
	// (only sectionRegen consumed it; retired in lockstep).
	oneUC, manyUC, genJobHandler, _ := buildScriptUseCases(
		cfg, root, normCfg, sourceReg, ppReg, clipSearchPort, clipSourceBuilder, log,
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

	// ── Admin token ────────────────────────────────────────────────────
	adminToken := ""
	if cfg != nil {
		adminToken = cfg.Security.AdminToken
	}

	// ── Step 5: Handler construction (slim form, PR-script-deps-slim) ──
	//
	// Preflight module removed; OutputSpec contains only the
	// surviving active flags. Callers sending removed flags receive
	// HTTP 400 UNKNOWN_FIELD at the wire boundary.

	submissionSvc, err := buildScriptSubmissionService(root, log)
	if err != nil {
		return fmt.Errorf("wireScriptFlow: build script submission service: %w", err)
	}
	remotionRenderer := &appvideo.HTTPRenderer{
		BaseURL: remotionBaseURL(),
		Client:  &http.Client{Timeout: 30 * time.Minute},
	}
	var drivePublisher delivery.Publisher
	if root.Drive != nil {
		drivePublisher = root.Drive.Publisher
	}
	if err := (appvideo.NewHandlerWithPublisher(remotionRenderer, drivePublisher, log)).Register(root.Jobs.Service); err != nil {
		return fmt.Errorf("wireScriptFlow: register Remotion render handler: %w", err)
	}
	remotionProducer := appvideo.NewProducer(root.Jobs.Facade)

	// Build the script-generation run repository when a DB is available.
	// This repository backs GET /jobs/:id/full and the durable runner.
	var runRepo scriptgen.RunRepository
	if root.DB != nil {
		repo, err := scriptgenrepo.NewSQLiteRunRepository(root.DB.DB, log)
		if err != nil {
			return fmt.Errorf("wireScriptFlow: build script generation run repository: %w", err)
		}
		runRepo = repo
		log.Info("wireScriptFlow: script generation run repository wired")
	} else {
		log.Warn("wireScriptFlow: root.DB is nil — script generation run repository not wired")
	}

	scriptDeps := scriptapi.Dependencies{
		Generate: scriptapi.GenerateDeps{
			Submission:    submissionSvc,
			GenRunStarter: nil, // wired below when runRepo is available
			Log:           log,
			Validator:     usecase.NewPayloadValidator(cfg.Scripts),
		},
		Shorts: scriptapi.ShortsDeps{
			Renderer: remotionRenderer,
			Producer: remotionProducer,
			Log:      log,
		},
		// FASE 2 (July 2026): JobsDeps.Registry is RETIRED. The
		// canonical MaxRetries lookup moved into
		// GenerationSubmissionService at composition time; JobsHandler
		// now owns only the GetJobStatus surface.
		Jobs: scriptapi.JobsDeps{
			Jobs:    root.Jobs.Facade,
			Log:     log,
			RunRepo: runRepo,
		},
		ClipsSearcher: clipsSearcher,
		AdminToken:    adminToken,
		EnabledFunc:   func() bool { return anyScriptFeatureEnabled(cfg) },
		ModuleOpts:    nil,
		Logger:        log,
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
	// PR-script-deps-slim: ScriptDescriptor.Handler field RETIRED;
	// Module is the canonical owner post-PR. tryRegisterModule
	// consumes Module only.
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
