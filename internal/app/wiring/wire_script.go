// Package app — wire_script.go canonicalises the ScriptFlow module wiring
// outside of the monolithic registry.go.
package wiring

import (
	"context"
	"fmt"
	"strings"

	scriptwiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/script"
	vidrushwiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/vidrush"
	vowiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/voiceover"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/channelprofile"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	opsapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/script"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	processor "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/processor"
	scriptjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/jobs"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/submission"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/gencore"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
	mediafinaljob "github.com/Marcuss-ops/PipelineGen/internal/platform/media/finaljob"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	ollamaadapters "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/adapters"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
	topicsourcecache "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/topicsourcecache"

	"go.uber.org/zap"
)

// visionVerdictClient returns the dedicated vision client when the AI bundle
// has one, falling back to the script-generation client only for partial
// wiring and legacy configurations. The composition root decides the fallback
// once at boot and logs it; this selector must not silently build a second
// client with a different budget.
func visionVerdictClient(root *ComposeRoot) *ollamaclient.Client {
	if root == nil || root.AI == nil {
		return nil
	}
	if root.AI.VisionVerdictClient != nil {
		return root.AI.VisionVerdictClient
	}
	if root.AI.ScriptGen == nil {
		return nil
	}
	return root.AI.ScriptGen.GetClient()
}

func scriptVisualCandidateScorer(root *ComposeRoot) adapters.SemanticCandidateScorer {
	if root == nil || root.AI == nil {
		return nil
	}
	if root.AI.VisionVerdictScorer != nil {
		return root.AI.VisionVerdictScorer
	}
	client := visionVerdictClient(root)
	if client == nil {
		return nil
	}
	return ollamaadapters.NewOllamaPersonImageScorer(client)
}

// wireScriptFlow constructs and registers the ScriptFlow module.
func wireScriptFlow(ctx context.Context, cfg *config.Config, log *zap.Logger, root *ComposeRoot, registry *module.Registry, searchFanOut assetsearch.SearchFanOut) error {
	_ = ctx
	if cfg == nil {
		return fmt.Errorf("wireScriptFlow: config is required")
	}
	if !cfg.Scripts.Capability.Enabled {
		log.Info("wireScriptFlow: script capability disabled by configuration")
		return nil
	}
	// Channel profiles: operator-editable per-channel render defaults
	// (subtitles, watermark, overlay style, SFX, mix policy, phrase-motion
	// pool) resolved at request build by BuildGenerateRequest. The file is
	// OPTIONAL — absent means "no profiles configured" and the feature stays
	// inert — while a file that exists and does not parse fails closed HERE,
	// before any job can render against a half-readable profile set. Editing
	// the file and restarting the server is the whole runtime knob: nothing
	// about RenderingGen changes, and no renderer redeploy is involved.
	if loaded, loadErr := channelprofile.LoadOptional(scriptwiring.ChannelProfilesPath()); loadErr != nil {
		return fmt.Errorf("wireScriptFlow: %w", loadErr)
	} else if loaded {
		log.Info("wireScriptFlow: channel profiles loaded", zap.Int("profiles", len(channelprofile.Snapshot())))
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

	setup := buildScriptSourceSetup(cfg, root, log)
	ppReg, vidRushProviders, vidRushFinalizer, vidRushCache, err := buildScriptPostProcessors(cfg, root, log, setup.sourceReg, setup.metaModel, searchFanOut)
	if err != nil {
		return err
	}
	normCfg, sourceReg, clipSourceBuilder, clipSearchPort, metaModel := setup.normCfg, setup.sourceReg, setup.clipSourceBuilder, setup.clipSearchPort, setup.metaModel
	// Use cases and job registration.
	oneUC, manyUC, genJobHandler, useCaseErr := buildScriptUseCases(
		cfg, root, normCfg, sourceReg, ppReg, clipSearchPort, clipSourceBuilder, log,
	)
	if useCaseErr != nil {
		return fmt.Errorf("wireScriptFlow: build use cases: %w", useCaseErr)
	}
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

	runtime, err := buildScriptFlowRuntime(cfg, root, log, oneUC, genJobHandler, vidRushProviders, vidRushFinalizer, vidRushCache)
	if err != nil {
		return err
	}
	return buildScriptFlowDescriptor(registry, log, cfg, root, oneUC, genJobHandler, runtime)
}

type scriptSourceSetup struct {
	normCfg           processor.NormalizationConfig
	sourceReg         *processor.SourceRegistry
	clipSourceBuilder *usecase.ClipSourceBuilder
	clipSearchPort    scriptports.AssetSearchPort
	metaModel         string
}

func buildScriptSourceSetup(cfg *config.Config, root *ComposeRoot, log *zap.Logger) *scriptSourceSetup {
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
	// P0-3/WST-02 (anti-muda): with OLLAMA_MAX_LOADED_MODELS=1 every model
	// switch evicts the resident runner (tens of seconds). The residency policy
	// is now ENFORCED, not logged: the generator maps every per-call model onto
	// cfg.External.OllamaModel, so a distinct metadata model no longer buys an
	// eviction — it is collapsed onto the resident one and the operator is told.
	if metaModel != "" && strings.TrimSpace(cfg.External.OllamaModel) != "" && metaModel != strings.TrimSpace(cfg.External.OllamaModel) {
		log.Warn("wireScriptFlow: distinct metadata model collapsed onto the single resident model (WST-02); set OLLAMA_METADATA_MODEL empty to silence this",
			zap.String("resident_model", cfg.External.OllamaModel), zap.String("metadata_model", metaModel))
	}

	return &scriptSourceSetup{
		normCfg:           normCfg,
		sourceReg:         sourceReg,
		clipSourceBuilder: clipSourceBuilder,
		clipSearchPort:    clipSearchPort,
		metaModel:         metaModel,
	}
}

func buildScriptPostProcessors(cfg *config.Config, root *ComposeRoot, log *zap.Logger, sourceReg *processor.SourceRegistry, metaModel string, searchFanOut assetsearch.SearchFanOut) (*adapters.PostProcessorRegistry, *adapters.VidRushAssetProviderRegistry, scriptports.VidRushArtifactFinalizer, scriptports.VidRushCachePort, error) {
	// Post-processors.
	scriptsRepoAdapter := sqlitescripts.NewRepositoryAdapter(root.Repos.ScriptsRepo)
	ppReg := adapters.NewPostProcessorRegistry(log)
	ppReg.SetCanonicalTimingAdapter(&adapters.CanonicalTimingAdapter{})
	vidRushProviders, vidRushFinalizer := (*adapters.VidRushAssetProviderRegistry)(nil), scriptports.VidRushArtifactFinalizer(nil)
	if root.Drive != nil && root.Drive.Publisher != nil && root.Outbox != nil && root.Outbox.EventsRepo != nil {
		vidRushDeps := vidrushwiring.VidRushMaterializationDeps{
			MediaPG: root.MediaPostgres,
			Delivery: vidrushwiring.VidRushDeliveryPorts{
				Publisher:  root.Drive.Publisher,
				EventsRepo: root.Outbox.EventsRepo,
			},
			ImageSearcher: vidrushInternetImageSearcher(root, log),
			MediaExec:     root.MediaExec,
		}
		vidRushProviders, vidRushFinalizer = vidrushwiring.BuildVidRushMaterialization(cfg, vidRushDeps, log)
	}
	vidRushCache := vidrushCachePort(root, log)
	if err := registerScriptPostProcessors(ppReg, root, cfg, log, scriptsRepoAdapter, metaModel, searchFanOut, vidRushProviders, vidRushFinalizer, vidRushCache); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("wireScriptFlow: %w", err)
	}
	sourceReg.Freeze()
	ppReg.Freeze()
	if err := validateRequiredProcessors(ppReg); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("wireScriptFlow: %w", err)
	}

	return ppReg, vidRushProviders, vidRushFinalizer, vidRushCache, nil
}

type scriptFlowRuntime struct {
	runRepo         scriptgen.RunRepository
	submissionSvc   *opsapp.Service
	clipsSearcher   scriptapi.ClipSearcher
	preflightAssets scriptapi.AssetPreflightReader
	adminToken      string
}

func buildScriptFlowRuntime(cfg *config.Config, root *ComposeRoot, log *zap.Logger, oneUC *gencore.GenerateOneUseCase, genJobHandler *scriptjobs.GenerateJobHandler, vidRushProviders *adapters.VidRushAssetProviderRegistry, vidRushFinalizer scriptports.VidRushArtifactFinalizer, vidRushCache scriptports.VidRushCachePort) (*scriptFlowRuntime, error) {
	var clipsSearcher scriptapi.ClipSearcher
	if root.MediaPostgres != nil {
		clipsSearcher = scriptwiring.NewClipsNameSearchAdapter(root.MediaPostgres)
	}

	// Stage 0, server-side (POST /api/script/preflight): the SAME operator
	// inventory read model the console serves, so the preflight cannot see a
	// different catalog than the operator inspecting the asset. Nil when the
	// media plane is closed — the route then answers unverified=true rather
	// than a false green.
	var preflightAssets scriptapi.AssetPreflightReader
	if root.MediaPostgres != nil {
		preflightAssets = newScriptAssetPreflightGate(pgmedia.NewOperatorInventoryReader(root.MediaPostgres, log))
	}

	adminToken := ""
	if cfg != nil {
		adminToken = cfg.Security.AdminToken
	}

	submissionSvc, err := buildScriptSubmissionService(root, log)
	if err != nil {
		return nil, fmt.Errorf("wireScriptFlow: build script submission service: %w", err)
	}

	// The run repository belongs to generation submission + durable runtime.
	// It is intentionally not threaded into the ScriptFlow HTTP jobs handler;
	// /api/jobs/:id/full is owned by the Jobs module.
	var runRepo scriptgen.RunRepository
	if root.ObservabilityDB != nil {
		repo, err := sqlitescripts.NewSQLiteRunRepository(root.ObservabilityDB.DB, log)
		if err != nil {
			return nil, fmt.Errorf("wireScriptFlow: build script generation run repository: %w", err)
		}
		runRepo = repo
		log.Info("wireScriptFlow: script generation run repository wired")
	} else {
		log.Warn("wireScriptFlow: root.DB is nil — script generation run repository not wired")
	}
	if runRepo == nil {
		return nil, fmt.Errorf("wireScriptFlow: script generation run repository is required for POST /api/script/generate")
	}
	genJobHandler.SetRunRepository(runRepo)

	if strings.TrimSpace(cfg.External.RustMusclesPath) != "" {
		var finalAudioCommitter assetspersistence.AssetCommitter
		if root.Outbox != nil && root.Outbox.EventsRepo != nil {
			if w, werr := newCanonicalAssetCommitter(root.MediaPostgres, log); werr != nil {
				return nil, fmt.Errorf("wireScriptFlow: canonical media writer: %w", werr)
			} else if w != nil {
				finalAudioCommitter = w
			}
		}
		durableRunner, runtimeErr := BuildScriptGenerationRuntime(cfg, root, runRepo, finalAudioCommitter, log, vidRushProviders, vidRushFinalizer, vidRushCache)
		if runtimeErr != nil {
			return nil, fmt.Errorf("wireScriptFlow: build durable script generation runtime: %w", runtimeErr)
		}
		genJobHandler.SetDurableRunner(durableRunner)
		if root.Jobs != nil && root.Jobs.Repo != nil {
			durableRunner.SetJobStageSink(root.Jobs.Repo)
		}
		root.ScriptGenerationRunner = durableRunner
		oneUC.SetOverlayBackgroundSource(durableRunner.OverlayBackgroundSource())
		oneUC.SetOverlayPlanCompiler(durableRunner.OverlayPlanCompiler())
		oneUC.SetPhraseAligner(durableRunner.PhraseAligner())
		if overlayEnqueuer := durableRunner.OverlayRenderEnqueuer(); overlayEnqueuer != nil {
			oneUC.SetOverlayRenderEnqueuer(overlayEnqueuer)
			log.Info("wireScriptFlow: batch script.generate_item overlay rendering wired through RenderingGen")
		} else {
			log.Warn("wireScriptFlow: batch overlay rendering unavailable (RenderingGen enqueuer not configured)")
		}
		wireLocalizedRenderEnqueuer(cfg, root, log, durableRunner)
		log.Info("wireScriptFlow: durable single-item runtime wired through canonical RenderPlan executor")
	} else {
		log.Warn("wireScriptFlow: Rust media executor path is empty; durable RenderPlan runtime is unavailable")
	}

	return &scriptFlowRuntime{
		runRepo:         runRepo,
		submissionSvc:   submissionSvc,
		clipsSearcher:   clipsSearcher,
		preflightAssets: preflightAssets,
		adminToken:      adminToken,
	}, nil
}

func buildScriptFlowDescriptor(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, oneUC *gencore.GenerateOneUseCase, genJobHandler *scriptjobs.GenerateJobHandler, runtime *scriptFlowRuntime) error {
	runRepo := runtime.runRepo
	submissionSvc := runtime.submissionSvc
	clipsSearcher := runtime.clipsSearcher
	preflightAssets := runtime.preflightAssets
	adminToken := runtime.adminToken

	scriptDeps := scriptapi.Dependencies{
		Generate: scriptapi.GenerateDeps{
			Submission:    submissionSvc,
			GenRunStarter: scriptgen.NewGenerationRunStarterWithRepo(nil, runRepo),
			Factory:       submission.NewSubmitRequestFactory(),
			Log:           log,
			Validator:     gencore.NewPayloadValidator(cfg.Scripts),
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
		Preflight:     scriptapi.PreflightDeps{Assets: preflightAssets},
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

func newRemoteFinalJobAdapter(root *ComposeRoot) (*scriptwiring.RemoteFinalJobAdapter, error) {
	if root == nil || root.MediaPostgres == nil || root.Drive == nil || root.Drive.Reader == nil {
		return nil, fmt.Errorf("remote final-job handoff requires the media SSOT and Drive stock reader")
	}
	return scriptwiring.NewRemoteFinalJobAdapterFromEnvironment(
		pgmedia.NewMediaSearcher(root.MediaPostgres), root.Drive.Reader,
	)
}

func wireScriptGenerationFinalJob(runner *scriptgen.Runner, cfg *config.Config, root *ComposeRoot, log *zap.Logger) error {
	if scriptwiring.FakeFinalJobRequested() {
		runner.SetFinalJobSubmitter(scriptwiring.NewFakeFinalJobSubmitter())
		log.Warn("remote final-job handoff SIMULATED (" + scriptwiring.FakeFinalJobEnv + "=1): no render will be dispatched; timings cover the LOCAL pipeline only")
		return nil
	}
	remoteFinalJob, err := newRemoteFinalJobAdapter(root)
	if err != nil {
		log.Warn("remote final-job handoff unavailable; final_job=true requests will fail closed", zap.Error(err))
		return nil
	}
	if cfg == nil || root == nil || strings.TrimSpace(cfg.External.FfmpegPath) == "" || strings.TrimSpace(cfg.External.RustMusclesPath) == "" {
		return fmt.Errorf("remote final-job requires the Rust media preflight, local non-stock fragment certifier, and configured ffmpeg")
	}
	fragmentLanes, err := mediafinaljob.FragmentLanes()
	if err != nil {
		return err
	}
	fragmentCacheDir := mediafinaljob.FragmentCacheDir(cfg.Storage.DataDir)
	remoteFinalJob.SetVideoSegmentPreparer(mediafinaljob.NewSegmentPreparer(cfg.External.FfmpegPath, mediaexec.EncoderPolicy{
		Codec: root.MediaExec.Policy.Codec, Preset: root.MediaExec.Policy.Preset, CRF: root.MediaExec.Policy.CRF,
	}, fragmentCacheDir, fragmentLanes, mediafinaljob.PublishFunc(scriptgen.PublishFinalJobLocalObjectVerified)))
	remoteFinalJob.SetMediaPreflighter(rustexec.NewClient(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log))
	runner.SetFinalJobSubmitter(remoteFinalJob)
	log.Info("remote final-job handoff wired", zap.String("master_url", remoteFinalJob.BaseURL()), zap.Int("non_stock_fragment_lanes", fragmentLanes), zap.Bool("fragment_cache", fragmentCacheDir != ""))
	return nil
}

func anyScriptFeatureEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.Features.ScriptClipsEnabled || cfg.Features.ImagesEnabled
}

func scriptGenerationEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.Scripts.Capability.Enabled
}

func registerScripts(ctx context.Context, registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, searchFanOut assetsearch.SearchFanOut) error {
	if err := wireScriptFlow(ctx, cfg, log, root, registry, searchFanOut); err != nil {
		return err
	}
	return registerScriptHistory(registry, log, cfg, root)
}

func vidrushCachePort(root *ComposeRoot, log *zap.Logger) scriptports.VidRushCachePort {
	if root == nil || root.CacheDB == nil || root.CacheDB.DB == nil {
		return nil
	}
	return vidrushwiring.BuildVidRushCache(root.CacheDB.DB, log)
}

func vidrushInternetImageSearcher(root *ComposeRoot, log *zap.Logger) adapters.InternetImageSearcher {
	if root == nil || root.Domains == nil || root.Domains.ImageSearchResolver == nil {
		return nil
	}
	// The cache lives on the adapter, so every consumer must receive the SAME
	// adapter instance. Before this singleton, the semantic planner, the
	// VidRush fan-out and the internet_images postprocessor each built their
	// own adapter (and cache), and the same person was searched three times.
	root.imageSearchAdapterOnce.Do(func() {
		root.imageSearchAdapter = newInternetImageSearchAdapter(root.Domains.ImageSearchResolver, log)
	})
	return root.imageSearchAdapter
}
