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
// to wire_script_postprocess.go; composition validators
// (validateScriptGenerateWiring,
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
// ScriptFlowDeps fields (Engine, Section, Image,
// Realtime, Association, Voiceover, AssetTree, ClipSourceBuilder,
// MediaCurator, Harvest, ScriptsRepo, DriveScriptsGenFolder,
// ClipServices) are RETIRED. The corresponding local-variable
// construction in wireScriptFlow (engine, harvestSvc, clipServices,
// ollamaTranslator, driveFolderClient, documentCreator) is
// dropped in lockstep. Retired handler adapters are no longer part of
// composition.

package app

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"net/http"
	"strings"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/processmetrics"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/shorts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/submission"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	appvideo "github.com/Marcuss-ops/PipelineGen/internal/application/video"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	sqliteprocessmetrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/processmetrics"
	scriptgenrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts/legacy"
	topicsourcecache "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/topicsourcecache"
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
// clip_bindings / visual_planning) to
// registerScriptPostProcessors in wire_script_postprocess.go. The
// orchestrator owns ppReg construction + ppReg.Freeze() +
// post-freeze required-processors validation; the registration
// cluster lives in the dedicated helper.
//
// PR-script-deps-slim (July 2026, P1): post-slim, the function
// no longer constructs the 12 ignored deps (Engine + Section +
// Image + Realtime + Association + Voiceover +
// AssetTree + ClipSourceBuilder + MediaCurator + Harvest +
// ScriptsRepo + DriveScriptsGenFolder + ClipServices). The
// slim Dependencies is 6 fields (Generate + Jobs +
// ClipsSearcher + AdminToken + 3 build-time). The 2 routes that
// depended on sectionRegen + cacheEviction (RegenerateSection +
// EvictCache) are RETIRED in lockstep.
func wireScriptFlow(ctx context.Context, cfg *config.Config, log *zap.Logger, root *wiring.ComposeRoot, registry *module.Registry, artlistWiring *wiring.ArtlistWiring) error {
	_ = ctx
	if cfg == nil {
		return fmt.Errorf("wireScriptFlow: config is required")
	}
	cap := cfg.Scripts.Capability
	if !cap.Enabled {
		log.Info("wireScriptFlow: script capability disabled by configuration")
		return nil
	}

	// Fail-closed dependency checks. In production a missing required
	// dependency aborts the boot. In dev mode (DeliveryInsecureDev) the
	// module is disabled explicitly and no route is registered.
	aiPresent := root.AI != nil && root.AI.ScriptGen != nil && root.AI.ScriptEngine != nil
	audioPresent := root.Domains != nil && root.Domains.AudioProcessor != nil
	if cap.RequireAI {
		if !aiPresent {
			if cfg.Security.DeliveryInsecureDev {
				log.Warn("wireScriptFlow: script capability disabled in dev — missing AI bundle (ScriptEngine)")
				return nil
			}
			return fmt.Errorf("wireScriptFlow: required AI bundle (ScriptEngine) is missing")
		}
		if !audioPresent {
			if cfg.Security.DeliveryInsecureDev {
				log.Warn("wireScriptFlow: script capability disabled in dev — missing audio processor")
				return nil
			}
			return fmt.Errorf("wireScriptFlow: required audio processor is missing")
		}
	} else if !aiPresent || !audioPresent {
		log.Warn("wireScriptFlow: AI bundle incomplete — disabling ScriptFlow without registering routes")
		return nil
	}
	if cap.RequireDrive {
		if root.Drive == nil {
			if cfg.Security.DeliveryInsecureDev {
				log.Warn("wireScriptFlow: script capability disabled in dev — missing Drive bundle")
				return nil
			}
			return fmt.Errorf("wireScriptFlow: required Drive bundle is missing")
		}
	}
	if cap.RequireDatabase {
		if root.DB == nil {
			if cfg.Security.DeliveryInsecureDev {
				log.Warn("wireScriptFlow: script capability disabled in dev — missing database")
				return nil
			}
			return fmt.Errorf("wireScriptFlow: required database is missing")
		}
	}

	// ── Wire wiring.ScriptVoiceoverGenerator (P1 verdetto) ─────────────────────
	// Constructs the VoiceoverGenerator adapter from the TTS audio processor
	// when available. Used by the script generation runner's Stage 4
	// (GENERATING_VOICEOVERS).
	if root.Domains != nil && root.Domains.AudioProcessor != nil {
		voPath := cfg.Storage.VoiceoversPath()
		root.AI.ScriptVoiceoverGenerator = wiring.NewScriptVoiceoverGenerator(root.Domains.AudioProcessor, voPath, log)
		log.Info("wireScriptFlow: wiring.ScriptVoiceoverGenerator wired",
			zap.String("output_dir", voPath))
	} else {
		log.Warn("wireScriptFlow: wiring.ScriptVoiceoverGenerator NOT wired (no audio processor) — voiceover stage will be skipped")
	}

	// ── Step 1: Source resolvers (factory in wire_script_resolvers.go) ──
	normCfg, sourceReg, clipSourceBuilder, clipSearchPort := buildScriptSourceResolvers(cfg, root, log)

	// Wire the source registry into the wiring.SceneTextGenerator so the
	// durable pipeline can resolve clip/catalog/search/curate sources.
	if root.AI != nil && root.AI.SceneTextGenerator != nil {
		root.AI.SceneTextGenerator.SetSourceRegistry(sourceReg)
	}

	// ── Pre-compute metadata model (used by post-processor + AI bundle) ──
	metaModel := strings.TrimSpace(cfg.External.OllamaModel)
	if mm := strings.TrimSpace(cfg.External.OllamaMetadataModel); mm != "" {
		metaModel = mm
	}

	// ── Step 2: Post-processor registration + freeze ────────────────────
	scriptsRepoAdapter := adapters.NewRepositoryAdapter(root.Repos.ScriptsRepo)
	ppReg := adapters.NewPostProcessorRegistry(log)
	if root.DB != nil && root.DB.DB != nil {
		legacyRepo := sqliteprocessmetrics.NewSQLiteRepository(root.DB.DB)
		canonicalProjection := appmetrics.NewRecorder(sqliteprocessmetrics.NewApplicationRepository(legacyRepo))
		ppReg.SetCanonicalTimingAdapter(&adapters.CanonicalTimingAdapter{
			ProcessMetrics: canonicalProjection,
		})
	}
	if err := registerScriptPostProcessors(ppReg, root, artlistWiring, cfg, log, scriptsRepoAdapter, metaModel); err != nil {
		return fmt.Errorf("wireScriptFlow: %w", err)
	}
	sourceReg.Freeze()
	ppReg.Freeze()
	if err := validateRequiredProcessors(ppReg); err != nil {
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
	if root.AI != nil && root.AI.ScriptGen != nil {
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
	remotionURL := cfg.Scripts.Capability.RemotionURL
	if remotionURL == "" {
		remotionURL = "http://127.0.0.1:4317"
	}
	renderTimeoutSeconds := cfg.Scripts.Capability.RenderTimeoutSeconds
	if renderTimeoutSeconds <= 0 {
		renderTimeoutSeconds = 1800
	}
	remotionRenderer := &appvideo.HTTPRenderer{
		BaseURL: remotionURL,
		Client:  &http.Client{Timeout: time.Duration(renderTimeoutSeconds) * time.Second},
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
		shorts.SetSubtitleArtifactRepository(root.Repos.SubtitleArtifactRepo)
		repo, err := scriptgenrepo.NewSQLiteRunRepository(root.DB.DB, log)
		if err != nil {
			return fmt.Errorf("wireScriptFlow: build script generation run repository: %w", err)
		}
		runRepo = repo
		log.Info("wireScriptFlow: script generation run repository wired")
	} else {
		log.Warn("wireScriptFlow: root.DB is nil — script generation run repository not wired")
	}
	if runRepo == nil {
		return fmt.Errorf("wireScriptFlow: script generation run repository is required for POST /api/script/generate")
	}

	scriptDeps := scriptapi.Dependencies{
		Generate: scriptapi.GenerateDeps{
			Submission:    submissionSvc,
			GenRunStarter: scriptgen.NewGenerationRunStarterWithRepo(nil, runRepo),
			Factory:       submission.NewSubmitRequestFactory(),
			Log:           log,
			Validator:     usecase.NewPayloadValidator(cfg.Scripts),
			ResearchPreflight: func() usecase.ResearchPreflight {
				if root.DB == nil {
					return nil
				}
				return usecase.NewResearchSubmissionPreflight(topicsourcecache.NewRepository(root.DB.DB))
			}(),
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
func registerScripts(ctx context.Context, registry *module.Registry, log *zap.Logger, cfg *config.Config, root *wiring.ComposeRoot, artlistWiring *wiring.ArtlistWiring) error {
	if err := wireScriptFlow(ctx, cfg, log, root, registry, artlistWiring); err != nil {
		return err
	}
	return registerScriptHistory(registry, log, cfg, root)
}
