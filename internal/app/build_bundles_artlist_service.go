package app

import (
	"fmt"

	"go.uber.org/zap"

	artlistapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets/adapters"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	scripts_usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func buildArtlistService(
	cfg *config.Config,
	log *zap.Logger,
	bundle *ArtlistBundle,
	dispatcher *outbox.Dispatcher,
	destResolver asset.Resolver,
	runtime *artlistRuntime,
) (*artlistPkg.Service, error) {
	service, err := artlistPkg.NewService(artlistPkg.ServiceDeps{
		ServicePorts: artlistPkg.ServicePorts{
			AssetStore:      bundle.ClipsRepo,
			Indexer:         bundle.ClipIndexerService,
			MetadataWriter:  runtime.metadataWriter,
			Publisher:       bundle.Publisher,
			ScraperSearcher: runtime.scraperSearcher,
			PixabaySearcher: runtime.pixabaySearcher,
			PexelsSearcher:  runtime.pexelsSearcher,
			Stager:          runtime.stager,
			IsLiveProbe:     runtime.isLiveProbe,
			RunRepository:   runtime.runRepository,
			SearchStrategy:  artlistPkg.ArtlistSearchStrategy(cfg.External.ArtlistSearchStrategy),
			SystemProber:    runtime.systemProber,
		},
		ServiceDependencies: artlistPkg.ServiceDependencies{
			Cfg:               cfg,
			Log:               log,
			MainDB:            bundle.DB.DB,
			Dispatcher:        dispatcher,
			MediaProcessor:    bundle.MediaProcessor,
			AssetDestResolver: destResolver,
			JobsSvc:           bundle.Jobs.Service,
			AssetProcRepo:     runtime.processingRepository,
			AssetVerRepo:      runtime.versionRepository,
			AssetFinalizerTx:  runtime.finalizer,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: artlist.NewService: %w", err)
	}
	return service, nil
}

func finalizeArtlistWiring(
	cfg *config.Config,
	log *zap.Logger,
	bundle *ArtlistBundle,
	service *artlistPkg.Service,
	runtime *artlistRuntime,
) (*ArtlistWiring, error) {
	bundle.ClipResolver = NewClipResolverRecommendAdapter(
		scripts_usecase.NewClipResolver(bundle.ClipsRepo, log),
		log,
	)

	descriptor, err := artlistapi.Build(artlistapi.Dependencies{
		Service:      service,
		CatalogSync:  bundle.CatalogSyncService,
		Jobs:         bundle.Jobs.Service,
		ClipResolver: bundle.ClipResolver,
		CfgPort:      newArtlistConfigAdapter(cfg),
		EnabledFunc:  func() bool { return cfg.Features.ArtlistEnabled },
		ModuleOpts:   nil,
		Logger:       log,
	})
	if err != nil {
		_ = service.Close()
		return nil, fmt.Errorf("WireArtlist: artlist.Build: %w", err)
	}

	ad, ok := descriptor.(*artlistapi.ArtlistDescriptor)
	if !ok || ad == nil {
		_ = service.Close()
		return nil, fmt.Errorf("WireArtlist: artlist.Build returned unexpected descriptor type %T (want *artlistapi.ArtlistDescriptor)", descriptor)
	}

	providerAssetsRegistry := providerassets.NewRegistry()
	_ = providerAssetsRegistry.Register(adapters.NewSearchProviderAdapter("artlist", artlistPkg.NewAdapter(service)))
	_ = providerAssetsRegistry.Register(adapters.NewSearcherAdapter("pexels", runtime.pexelsSearcher))
	_ = providerAssetsRegistry.Register(adapters.NewSearcherAdapter("pixabay", runtime.pixabaySearcher))
	providerAssetsRegistry.Freeze()

	log.Info("WireArtlist: ART-001 reversal wiring complete",
		zap.String("descriptor_name", ad.Name()),
		zap.Strings("provider_assets", providerAssetsRegistry.Names()),
		zap.Bool("godlike_06_ssot", true),
	)
	return &ArtlistWiring{
		Module:         ad.Module,
		Service:        ad.Service,
		ProviderAssets: providerAssetsRegistry,
		LicenseRepo:    runtime.licenseRepository,
		ReleaseRepo:    runtime.releaseRepository,
		RenditionRepo:  runtime.renditionRepository,
	}, nil
}
