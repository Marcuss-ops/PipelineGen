package wiring

import (
	"fmt"
	"os"

	wiringaudio "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	assetregister "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/register"
	assetsfx "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/soundeffect"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

import (
	assetswiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/assets"
	infradrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)
import (
	assetvoice "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/voiceover"
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

	// Media SSOT read port for the clips capability. PostgreSQL wins whenever
	// the media plane is open; the SQLite adapter is the documented
	// media-disabled degrade fallback. The type is the clips capability's
	// consumer-owned interface, so no caller depends on the SQLite-only
	// detail.Repository type-switch bridge.
	var assetRepo clipsapi.AssetReader
	// MediaSearch answers the ListClips text-search branch the same way: the
	// PostgreSQL media read authority when the media plane is open, nil (fail
	// closed) when it is not. The retired SQLite branch read the operational
	// clip_search_terms index, which the canonical committer never populates.
	var mediaSearch clipsapi.MediaClipSearcher
	switch {
	case deps.MediaPostgres != nil:
		mediaSearcher := pgmedia.NewMediaSearcher(deps.MediaPostgres)
		assetRepo = pgmedia.NewMediaClipAssetReader(mediaSearcher)
		mediaSearch = newPostgresClipMediaSearch(mediaSearcher)
	case deps.Core.Services.Assets != nil:
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
				MediaSearch:   mediaSearch,
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

	rd, err := buildRegisterBundle(cfg, log, deps, textTrackRepo, lifecycle, driveUploader, providerRegistry, clipEnricher, idemHandler, dispatcher, jobs, deps.MaterializeFanOut)
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
		DownloadClip:         clipsDesc.DownloadClip,
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
	sfxAdapters := wiringaudio.NewSoundEffectAdapters(
		params.Soundeffect.ClipsRepo,
		params.Soundeffect.MetaWriter,
		params.Soundeffect.Dispatcher,
		"data",
	)

	descriptor, err := assetsfx.Build(assetsfx.Dependencies{
		Core: assetsfx.CoreDeps{
			ClipsRepo:     sfxAdapters.Clips,
			MetaWriter:    sfxAdapters.Metadata,
			ProcessRunner: processRunnerAdapter,
		},
		Delivery: assetsfx.DeliveryDeps{
			Resolver:               sfxAdapters.Destination,
			Publisher:              params.Soundeffect.Publisher,
			SoundEffectsRootFolder: params.Cfg.Drive.SoundEffectsRootFolder,
		},
		Transport: assetsfx.TransportDeps{
			Dispatcher:  sfxAdapters.Dispatcher,
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
	materializeFanOut *texttracks.MaterializeFanOut,
) (*assetregister.RegisterDescriptor, error) {
	sourcingClipStore := newSourcingClipStore(deps.MediaPostgres)
	// 2026-09-17: the canonical atomic terminal write for the register path is
	// the same PostgreSQL committer the extraction path uses. Resolved from the
	// media SSOT handle; nil (media PostgreSQL disabled) leaves the legacy split
	// path in place, which the register service already treats as its fallback.
	registerAtomicWriter := canonicalRegisterAtomicWriter(deps.MediaPostgres, log)
	registerSvc := newAssetRegisterService(cfg, log, sourcingClipStore, textTrackRepo, driveUploader, lifecycle, deps.Core.Services.AssetTreeService, providerRegistry, clipEnricher, dispatcher, deps.Delivery.Publisher, jobs.Service, registerAtomicWriter, materializeFanOut)

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
			return fmt.Errorf("drive credentials missing at driveChecker invocation: %w (PR-DRIVE-AVAILABILITY-GATE fail-closed; rerun python3 scripts/generate_drive_token.py to fix)", err)
		}
		if _, err := os.Stat(cfg.GetTokenPath()); err != nil {
			return fmt.Errorf("drive token missing at driveChecker invocation: %w (PR-DRIVE-AVAILABILITY-GATE fail-closed; rerun python3 scripts/generate_drive_token.py to fix)", err)
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

// Compatibility aliases keep the composition root stable while ownership of
// the resolver lives in wiring/assets. New code should import the leaf package.
type AssetDestResolver = assetswiring.DestResolver

var (
	ErrAssetDestResolverNotWired = assetswiring.ErrDestResolverNotWired
	ErrAssetDestParentRequired   = assetswiring.ErrDestParentRequired
	ErrAssetDestSubfolderFailed  = assetswiring.ErrDestSubfolderFailed
	ErrAssetDestEmptyFolderID    = assetswiring.ErrDestEmptyFolderID
)

func NewAssetDestResolver(admin infradrive.Admin) *AssetDestResolver {
	return assetswiring.NewDestResolver(admin)
}

// buildVoiceoverBundle constructs the canonical *assetvoice.VoiceoverDescriptor.
//
// Blocco C1-Step 7 (June 2026): the Handler is constructed inside
// Build and captured by the returned VoiceoverDescriptor's Module
// closure. The composition site type-asserts ONCE to
// *assetvoice.VoiceoverDescriptor (fail-closed) and reuses the
// concrete for the assetsapi.NewModule(..., Voiceover: vd, ...) call
// (the concrete *VoiceoverDescriptor satisfies api.Descriptor
// structurally). The voiceover capability has no non-HTTP consumer in
// the codebase (/generate is the entire public surface, reachable only
// via HTTP), so the Descriptor surface is the smallest possible — just
// `Module` field + forwarder methods (matches the stock precedent
// exactly).
//
// voiceover is always on in production (no feature flag) and has no
// per-feature middleware.
func buildVoiceoverBundle(
	log *zap.Logger,
	jobs *JobsBundle,
) (*assetvoice.VoiceoverDescriptor, error) {
	descriptor, err := assetvoice.Build(assetvoice.Dependencies{
		Jobs:        jobs.Facade,
		EnabledFunc: func() bool { return true }, // voiceover is always on in production (no feature flag)
		ModuleOpts:  nil,                         // no per-feature middleware for the voiceover capability (matches pre-Step-7 wiring)
		Logger:      log,
	})
	if err != nil {
		return nil, err
	}
	desc, ok := descriptor.(*assetvoice.VoiceoverDescriptor)
	if err := ClassifyDepGet(fmt.Sprintf("WireAssets: voiceover (got %T, want *assetvoice.VoiceoverDescriptor)", descriptor), !ok || desc == nil, DepRequired, log); err != nil {
		return nil, err
	}
	return desc, nil
}
