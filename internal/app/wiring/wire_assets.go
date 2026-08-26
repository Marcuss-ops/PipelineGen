package wiring

import (
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	assetregister "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/register"
	assetsfx "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/soundeffect"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// WireAssets builds the Assets HTTP capability from dependencies that were
// fully constructed by the composition root. Required services are immutable:
// the function never creates a second deletion service and never participates
// in post-construction setter injection.
func WireAssets(
	cfg *config.Config,
	log *zap.Logger,
	deps *AssetsModuleDeps,
	textTrackRepo detail.TextTrackRepository,
	jobs *JobsBundle,
	lifecycle driveutil.FileLifecycle,
	providerRegistry *providers.Registry,
	dispatcher *outbox.Dispatcher,
	deletionSvc *deletion.DeletionService,
) (*AssetsWiring, error) {
	if deletionSvc == nil {
		return nil, fmt.Errorf("WireAssets: canonical deletion service is required")
	}

	var driveUploader *driveutil.Uploader
	if deps.Delivery.Admin != nil {
		if up, ok := deps.Delivery.Admin.(*driveutil.Uploader); ok {
			if up == nil {
				return nil, fmt.Errorf("WireAssets: drive.Admin: direct *driveutil.Uploader is nil (typed-NIL interface trap; PR-ADAPTER-NIL-GUARD fail-closed)")
			}
			driveUploader = up
		} else if adapter, ok := deps.Delivery.Admin.(*driveutil.AdminAdapter); ok {
			if adapter.Uploader == nil {
				return nil, driveutil.WrapDriveAdminError(driveutil.ErrAdminAdapterUploaderNil)
			}
			driveUploader = adapter.Uploader
		} else {
			return nil, driveutil.WrapDriveAdminError(driveutil.ErrAdminUnknownType)
		}
	}

	var assetRepo detail.Repository
	if deps.Core.Services.Assets != nil {
		assetRepo = deps.Core.Services.Assets.Repository()
	}

	searchBackends := deps.Search.SearchBackendRegistry
	searchFanOut := deps.Search.SearchFanOut
	searchAggregator := deps.Search.SearchAggregator
	if err := ClassifyDepGet("WireAssets: deps.Search.SearchFanOut is nil (composition root must call BuildCanonicalSearchFanOut before WireAssets)", searchFanOut == nil, DepRequired, log); err != nil {
		return nil, err
	}
	if err := ClassifyDepGet("WireAssets: deps.Search.SearchAggregator is nil (composition root must call BuildCanonicalSearchFanOut before WireAssets)", searchAggregator == nil, DepRequired, log); err != nil {
		return nil, err
	}
	log.Info("WireAssets: consumed pre-built canonical SearchFanOut",
		zap.Int("backends", len(searchBackends.All())))

	metaWriter := semantic.NewNopMetadataWriter(log)

	var idemHandler gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if deps.Background.IdempotencyStore != nil {
		idemHandler = deps.Background.IdempotencyStoreHandler
	}

	clipsDesc, clipEnricher, err := buildClipsBundle(buildClipsParams{
		Cfg: cfg,
		Log: log,
		Clips: ClipsCapabilityDeps{
			Repositories: ClipsRepositoryDeps{
				ClipsRepo:     deps.Core.Repositories.ClipsRepo,
				VoiceoverRepo: deps.Core.Repositories.VoiceoverRepo,
				ImageRepo:     deps.Core.Repositories.ImageRepo,
				AssetRepo:     assetRepo,
			},
			ArtifactService:    deps.Core.Services.ArtifactService,
			AssetTreeService:   deps.Core.Services.AssetTreeService,
			MediaProcessor:     deps.Core.Services.MediaProcessor,
			Publisher:          deps.Delivery.Publisher,
			ClipIndexerService: deps.Search.ClipIndexerService,
		},
		Jobs:          jobs,
		Dispatcher:    dispatcher,
		DriveUploader: driveUploader,
		MetaWriter:    metaWriter,
		DeletionSvc:   deletionSvc,
		IdemHandler:   idemHandler,
	})
	if err != nil {
		return nil, fmt.Errorf("WireAssets: clips: %w", err)
	}

	log.Info("WireAssets: pre-DescriptorJobs-registration jobs-service snapshot",
		zap.String("jobs_service_type", fmt.Sprintf("%T", jobs.Service)),
		zap.String("jobs_service_ptr", fmt.Sprintf("%p", jobs.Service)),
		zap.String("jobs_facade_type", fmt.Sprintf("%T", jobs.Facade)),
		zap.String("jobs_facade_ptr", fmt.Sprintf("%p", jobs.Facade)),
		zap.String("clipsDesc_type", fmt.Sprintf("%T", clipsDesc)),
		zap.String("clipsDesc_ptr", fmt.Sprintf("%p", clipsDesc)),
	)
	if dj, ok := any(clipsDesc).(module.DescriptorJobHandlers); ok {
		log.Info("WireAssets: clipsDesc type-asserted as runtime DescriptorJobHandlers; publishing owned jobs")
		if err := module.RegisterRuntimeJobHandlers(jobs.Service, dj); err != nil {
			return nil, fmt.Errorf("WireAssets: clips runtime job descriptors: %w", err)
		}
	} else if dj, ok := any(clipsDesc).(module.DescriptorJobs); ok {
		// Compatibility fallback for descriptors that have not yet exposed
		// enumerable handlers. New capability modules must prefer the
		// descriptor list above so job ownership is inspectable before boot.
		log.Info("WireAssets: clipsDesc uses compatibility DescriptorJobs slot")
		if err := dj.RegisterJobHandlers(jobs.Service); err != nil {
			return nil, fmt.Errorf("WireAssets: clips compatibility job registration: %w", err)
		}
	} else {
		log.Warn("WireAssets: clipsDesc does NOT expose runtime job descriptors — bulk_upload_youtube_clips will be unhandled",
			zap.String("clipsDesc_type", fmt.Sprintf("%T", clipsDesc)),
			zap.String("clipsDesc_ptr", fmt.Sprintf("%p", clipsDesc)),
		)
	}

	storageDesc, err := buildStorageBundle(log, jobs, deps.Core.Services.CatalogSyncService)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: storage: %w", err)
	}

	dd, diagSvc, err := buildDiagnosticsBundle(log, deps.Core.Repositories.ClipsRepo)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: diagnostics: %w", err)
	}

	sd, err := buildSearchBundle(log, searchAggregator, providerRegistry)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: search: %w", err)
	}
	if diagSvc != nil {
		log.Info("diagnostics and search services wired with production ports")
	} else {
		log.Warn("diagnostics service NOT fully wired — some routes will return 503")
	}

	vd, err := buildVoiceoverBundle(log, jobs)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: voiceover: %w", err)
	}

	soundeffectDesc, err := buildSoundeffectBundle(buildSoundeffectParams{
		Cfg: cfg,
		Log: log,
		Soundeffect: SoundeffectCapabilityDeps{
			ClipsRepo:  deps.Core.Repositories.ClipsRepo,
			MetaWriter: metaWriter,
			Publisher:  deps.Delivery.Publisher,
			Dispatcher: dispatcher,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("WireAssets: soundeffect: %w", err)
	}

	rd, err := buildRegisterBundle(cfg, log, deps, textTrackRepo, lifecycle, driveUploader, providerRegistry, clipEnricher, idemHandler, dispatcher, jobs)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: register: %w", err)
	}

	assetsMod := assetsapi.NewModule(assetsapi.Dependencies{
		Storage:     storageDesc,
		Diagnostics: dd,
		Search:      sd,
		Clips:       clipsDesc,
		Voiceover:   vd,
		SoundEffect: soundeffectDesc,
		Register:    rd,
	}, log)
	assetsRouteMod := module.NewRouteModule(
		"assets",
		func() bool { return true },
		"/media",
		assetsMod,
		log,
	)
	log.Info("created unified Assets module (thin transport)")

	return &AssetsWiring{
		Module:               assetsRouteMod,
		DeletionSvc:          deletionSvc,
		InternalMediaHandler: storageDesc.Handler,
		SearchAggregator:     searchAggregator,
		SearchFanOut:         searchFanOut,
	}, nil
}

// SoundeffectCapabilityDeps contains only the ports consumed by the
// soundeffect capability builder. WireAssets projects AssetsModuleDeps into
// this bundle at the composition boundary; the broad module container does
// not cross into the builder.
type SoundeffectCapabilityDeps struct {
	ClipsRepo  *assets.ClipsRepository
	MetaWriter semantic.MetadataWriterPort
	Publisher  delivery.Publisher
	Dispatcher *outbox.Dispatcher
}

type buildSoundeffectParams struct {
	Cfg         *config.Config
	Log         *zap.Logger
	Soundeffect SoundeffectCapabilityDeps
}

func buildSoundeffectBundle(params buildSoundeffectParams) (*assetsfx.SoundeffectDescriptor, error) {
	if params.Cfg == nil {
		return nil, fmt.Errorf("soundeffect: config is required")
	}
	sfxClips := &sfxClipsRepoAdapter{repo: params.Soundeffect.ClipsRepo}
	sfxMeta := &sfxSemanticWriterAdapter{w: params.Soundeffect.MetaWriter}
	sfxResolver := &sfxResolverAdapter{mediaRoot: "data"}
	sfxDispatcher := newSfxDispatcherAdapter(params.Soundeffect.Dispatcher)

	descriptor, err := assetsfx.Build(assetsfx.Dependencies{
		Core: assetsfx.CoreDeps{
			ClipsRepo:     sfxClips,
			MetaWriter:    sfxMeta,
			ProcessRunner: processRunnerAdapter,
		},
		Delivery: assetsfx.DeliveryDeps{
			Resolver:               sfxResolver,
			Publisher:              params.Soundeffect.Publisher,
			SoundEffectsRootFolder: params.Cfg.Drive.SoundEffectsRootFolder,
		},
		Transport: assetsfx.TransportDeps{
			Dispatcher:  sfxDispatcher,
			EnabledFunc: func() bool { return true },
			ModuleOpts:  nil,
		},
		Observability: assetsfx.ObservabilityDeps{
			Logger: params.Log,
		},
	})
	if err != nil {
		return nil, err
	}
	desc, ok := descriptor.(*assetsfx.SoundeffectDescriptor)
	if err := ClassifyDepGet(fmt.Sprintf("WireAssets: soundeffect (got %T, want *assetsfx.SoundeffectDescriptor)", descriptor), !ok || desc == nil, DepRequired, params.Log); err != nil {
		return nil, err
	}
	return desc, nil
}

func buildRegisterBundle(
	cfg *config.Config,
	log *zap.Logger,
	deps *AssetsModuleDeps,
	textTrackRepo detail.TextTrackRepository,
	lifecycle driveutil.FileLifecycle,
	driveUploader *driveutil.Uploader,
	providerRegistry *providers.Registry,
	clipEnricher appclips.ClipEnricher,
	idemHandler gin.HandlerFunc,
	dispatcher *outbox.Dispatcher,
	jobs *JobsBundle,
) (*assetregister.RegisterDescriptor, error) {
	registerSvc := newAssetRegisterService(cfg, log, deps.Core.Repositories.ClipsRepo, textTrackRepo, driveUploader, lifecycle, deps.Core.Services.AssetTreeService, providerRegistry, clipEnricher, dispatcher, deps.Delivery.Publisher, jobs.Service)

	driveChecker := func() error {
		if driveUploader == nil {
			return fmt.Errorf("drive uploader not wired at composition time (build_bundles_drive.go::BuildDriveBundle returned driveClient==nil; PR-DRIVE-AVAILABILITY-GATE fail-closed forbids folder_id traffic)")
		}
		if driveUploader.Service == nil {
			return fmt.Errorf("driveUploader.Service is nil despite driveUploader non-nil (typed-NIL interface trap; PR-DRIVE-AVAILABILITY-GATE fail-closed forbids folder_id traffic)")
		}
		if cfg == nil {
			return fmt.Errorf("cfg is nil at driveChecker invocation (composition root invariant violated; PR-DRIVE-AVAILABILITY-GATE fail-closed)")
		}
		if _, err := os.Stat(cfg.GetCredentialsPath()); err != nil {
			return fmt.Errorf("Drive credentials missing at driveChecker invocation: %w (PR-DRIVE-AVAILABILITY-GATE fail-closed; rerun python3 scripts/generate_drive_token.py to fix)", err)
		}
		if _, err := os.Stat(cfg.GetTokenPath()); err != nil {
			return fmt.Errorf("Drive token missing at driveChecker invocation: %w (PR-DRIVE-AVAILABILITY-GATE fail-closed; rerun python3 scripts/generate_drive_token.py to fix)", err)
		}
		return nil
	}

	descriptor, err := assetregister.Build(assetregister.Dependencies{
		Service:      registerSvc,
		Idempotency:  idemHandler,
		EnabledFunc:  func() bool { return true },
		ModuleOpts:   nil,
		Logger:       log,
		DriveChecker: driveChecker,
	})
	if err != nil {
		return nil, err
	}
	desc, ok := descriptor.(*assetregister.RegisterDescriptor)
	if err := ClassifyDepGet(fmt.Sprintf("WireAssets: register (got %T, want *assetregister.RegisterDescriptor)", descriptor), !ok || desc == nil, DepRequired, log); err != nil {
		return nil, err
	}
	return desc, nil
}
