// Package app — wire_script.go canonicalises the ScriptFlow module wiring
// outside of the monolithic registry.go.
package wiring

import (
	"context"
	"fmt"
	"strings"

	vidrushwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/vidrush"
	vowiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/voiceover"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/script"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/submission"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	scriptgenrepo "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
	topicsourcecache "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/topicsourcecache"

	"go.uber.org/zap"
)

// wireScriptFlow constructs and registers the ScriptFlow module.
func wireScriptFlow(ctx context.Context, cfg *config.Config, log *zap.Logger, root *ComposeRoot, registry *module.Registry, artlistWiring *ArtlistWiring) error {
	_ = ctx
	if cfg == nil {
		return fmt.Errorf("wireScriptFlow: config is required")
	}
	if !cfg.Scripts.Capability.Enabled {
		log.Info("wireScriptFlow: script capability disabled by configuration")
		return nil
	}
	ready, err := validateScriptFlowDependencies(cfg, root, log)
	if err != nil {
		return err
	}
	if !ready {
		return nil
	}

	// Wire ScriptVoiceoverGenerator when the canonical per-item voiceover
	// pipeline is available.
	if root.Domains != nil && root.Domains.VoiceoverProcessItem != nil {
		voPath := cfg.Storage.VoiceoversPath()
		voiceGenerator := vowiring.NewScriptVoiceoverGenerator(root.Domains.VoiceoverProcessItem, voPath, log)
		voiceMap := make(map[string]string)
		if registry, registryErr := BuildLanguageRegistry(ActiveMultilingualConfig(cfg)); registryErr == nil {
			for _, spec := range registry.EnabledLanguages() {
				if spec.EdgeTTSVoice != "" {
					voiceMap[spec.Code] = spec.EdgeTTSVoice
				}
			}
		}
		voiceGenerator.ConfigureVoices(voiceMap)
		root.AI.ScriptVoiceoverGenerator = voiceGenerator
		log.Info("wireScriptFlow: ScriptVoiceoverGenerator wired", zap.String("output_dir", voPath))
	} else {
		log.Warn("wireScriptFlow: ScriptVoiceoverGenerator NOT wired (no voiceover pipeline) — voiceover stage will be skipped")
	}

	// Source resolvers.
	normCfg, sourceReg, clipSourceBuilder, clipSearchPort := buildScriptSourceResolvers(cfg, root, log)
	if root.AI != nil && root.AI.SceneTextGenerator != nil {
		root.AI.SceneTextGenerator.SetSourceRegistry(sourceReg)
		if strings.TrimSpace(cfg.External.RustMusclesPath) != "" {
			prober := rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
			root.AI.SceneTextGenerator.SetClipProber(prober)
			log.Info("wireScriptFlow: SceneTextGenerator clip prober wired")
		} else {
			log.Warn("wireScriptFlow: Rust media executor path empty; SceneTextGenerator duration probe unavailable")
		}
	}

	metaModel := strings.TrimSpace(cfg.External.OllamaModel)
	if mm := strings.TrimSpace(cfg.External.OllamaMetadataModel); mm != "" {
		metaModel = mm
	}

	// Post-processors.
	scriptsRepoAdapter := sqlitescripts.NewRepositoryAdapter(root.Repos.ScriptsRepo)
	ppReg := adapters.NewPostProcessorRegistry(log)
	ppReg.SetCanonicalTimingAdapter(&adapters.CanonicalTimingAdapter{})
	vidRushProviders, vidRushFinalizer := (*adapters.VidRushAssetProviderRegistry)(nil), scriptports.VidRushArtifactFinalizer(nil)
	if root.Drive != nil && root.Drive.Publisher != nil && root.Outbox != nil && root.Outbox.EventsRepo != nil {
		vidRushProviders, vidRushFinalizer = vidrushwiring.BuildVidRushMaterialization(cfg, vidrushwiring.VidRushMaterializationDeps{
			MediaPG:     root.MediaPostgres,
			MediaSQLite: root.DB.DB,
			Delivery: vidrushwiring.VidRushDeliveryPorts{
				Publisher:      root.Drive.Publisher,
				EventsRepo:     root.Outbox.EventsRepo,
				ProviderAssets: artlistWiring.ProviderAssets,
				Downloader:     artlistWiring.ArtlistDownloader,
			},
			ImageSearcher:  vidrushInternetImageSearcher(root, log),
			ImageGenerator: root.Domains.ImageService,
			MediaExec:      root.MediaExec,
		}, log)
	}
	vidRushCache := vidrushCachePort(root, log)
	if err := registerScriptPostProcessors(ppReg, root, artlistWiring, cfg, log, scriptsRepoAdapter, metaModel, vidRushProviders, vidRushFinalizer, vidRushCache); err != nil {
		return fmt.Errorf("wireScriptFlow: %w", err)
	}
	sourceReg.Freeze()
	ppReg.Freeze()
	if err := validateRequiredProcessors(ppReg); err != nil {
		return fmt.Errorf("wireScriptFlow: %w", err)
	}

	// Use cases and job registration.
	oneUC, manyUC, genJobHandler, _ := buildScriptUseCases(
		cfg, root, normCfg, sourceReg, ppReg, clipSearchPort, clipSourceBuilder, log,
	)
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

	if root.AI != nil && root.AI.ScriptGen != nil {
		root.AI.ScriptGen.SetMetadataModel(metaModel)
	}

	var clipsSearcher scriptapi.ClipSearcher
	if root.Repos.ClipsRepo != nil {
		clipsSearcher = &clipsNameSearchAdapter{repo: root.Repos.ClipsRepo}
	}

	adminToken := ""
	if cfg != nil {
		adminToken = cfg.Security.AdminToken
	}

	submissionSvc, err := buildScriptSubmissionService(root, log)
	if err != nil {
		return fmt.Errorf("wireScriptFlow: build script submission service: %w", err)
	}

	// The run repository belongs to generation submission + durable runtime.
	// It is intentionally not threaded into the ScriptFlow HTTP jobs handler;
	// /api/jobs/:id/full is owned by the Jobs module.
	var runRepo scriptgen.RunRepository
	if root.ObservabilityDB != nil {
		repo, err := scriptgenrepo.NewSQLiteRunRepository(root.ObservabilityDB.DB, log)
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
	genJobHandler.SetRunRepository(runRepo)

	if strings.TrimSpace(cfg.External.RustMusclesPath) != "" {
		var finalAudioCommitter assetspersistence.AssetCommitter
		if root.Outbox != nil && root.Outbox.EventsRepo != nil {
			if w, werr := newCanonicalAssetCommitter(root.MediaPostgres, log); werr != nil {
				return fmt.Errorf("wireScriptFlow: canonical media writer: %w", werr)
			} else if w != nil {
				finalAudioCommitter = w
			}
		}
		durableRunner, runtimeErr := BuildScriptGenerationRuntime(cfg, root, runRepo, finalAudioCommitter, log, vidRushProviders, vidRushFinalizer, vidRushCache)
		if runtimeErr != nil {
			return fmt.Errorf("wireScriptFlow: build durable script generation runtime: %w", runtimeErr)
		}
		genJobHandler.SetDurableRunner(durableRunner)
		wireLocalizedRenderEnqueuer(cfg, root, log, durableRunner)
		log.Info("wireScriptFlow: durable single-item runtime wired through canonical RenderPlan executor")
	} else {
		log.Warn("wireScriptFlow: Rust media executor path is empty; durable RenderPlan runtime is unavailable")
	}

	scriptDeps := scriptapi.Dependencies{
		Generate: scriptapi.GenerateDeps{
			Submission:    submissionSvc,
			GenRunStarter: scriptgen.NewGenerationRunStarterWithRepo(nil, runRepo),
			Factory:       submission.NewSubmitRequestFactory(),
			Log:           log,
			Validator:     usecase.NewPayloadValidator(cfg.Scripts),
			ResearchPreflight: func() usecase.ResearchPreflight {
				if root.CacheDB == nil || root.CacheDB.DB == nil {
					return nil
				}
				preflight := usecase.NewResearchSubmissionPreflight(topicsourcecache.NewRepository(root.CacheDB.DB))
				preflight.SetResearchPolicyVersion(researchPolicyVersion(cfg))
				return preflight
			}(),
		},
		Jobs: scriptapi.JobsDeps{
			Jobs: root.Jobs.Facade,
			Log:  log,
		},
		ClipsSearcher: clipsSearcher,
		AdminToken:    adminToken,
		EnabledFunc:   func() bool { return scriptGenerationEnabled(cfg) },
		ModuleOpts: []module.RouteModuleOption{
			module.WithMiddleware(middleware.RequireAdminToken(cfg, log)),
		},
		Logger: log,
	}
	scriptDescriptor, err := scriptapi.Build(scriptDeps)
	if err != nil {
		return fmt.Errorf("wireScriptFlow: %w", err)
	}
	sd, ok := scriptDescriptor.(*scriptapi.ScriptDescriptor)
	if !ok || sd == nil {
		return fmt.Errorf("wireScriptFlow: script.Build returned unexpected descriptor type %T (want *scriptapi.ScriptDescriptor)", scriptDescriptor)
	}
	return tryRegisterModule(registry, log, sd)
}

// anyScriptFeatureEnabled returns true when at least one script feature flag
// is on.
func anyScriptFeatureEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.Features.ScriptClipsEnabled || cfg.Features.ImagesEnabled
}

// scriptGenerationEnabled is the dedicated gate for POST /api/script/generate.
func scriptGenerationEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.Scripts.Capability.Enabled
}

// registerScripts orchestrates the /api/script/* routing surface.
func registerScripts(ctx context.Context, registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, artlistWiring *ArtlistWiring) error {
	if err := wireScriptFlow(ctx, cfg, log, root, registry, artlistWiring); err != nil {
		return err
	}
	return registerScriptHistory(registry, log, cfg, root)
}

// vidrushCachePort resolves the nil-tolerant VidRush cache port from the
// composition root. A missing cache plane disables the cache (nil = off).
func vidrushCachePort(root *ComposeRoot, log *zap.Logger) scriptports.VidRushCachePort {
	if root == nil || root.CacheDB == nil || root.CacheDB.DB == nil {
		return nil
	}
	return vidrushwiring.BuildVidRushCache(root.CacheDB.DB, log)
}

// vidrushInternetImageSearcher adapts the root image-search resolver into
// the InternetImageSearcher port consumed by the VidRush materialization
// wiring. Nil-safe: returns nil when the resolver is not wired.
func vidrushInternetImageSearcher(root *ComposeRoot, log *zap.Logger) adapters.InternetImageSearcher {
	if root == nil || root.Domains == nil || root.Domains.ImageSearchResolver == nil {
		return nil
	}
	return newInternetImageSearchAdapter(root.Domains.ImageSearchResolver, log)
}
