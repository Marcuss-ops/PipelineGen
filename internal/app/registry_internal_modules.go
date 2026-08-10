package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	youtubeapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	artlistadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	stockadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock"
	youtubeadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/youtube"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptassets"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	appimages "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	capjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/embeddings"
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

func registerInternalModules(ctx context.Context, registry *module.Registry, log *zap.Logger, cfg *config.Config, root *wiring.ComposeRoot, regWiring *RegistryWiring) (registryCrossStepState, error) {
	idemPlus := middleware.NewIdempotency(root.Repos.IdempotencyStore, log)
	idemHandler := idemPlus.Handler()

	var providerReg *providers.Registry
	if root.Search != nil {
		providerReg = root.Search.ProviderRegistry
	}

	var vectorStoreForSearch assetsearch.VectorStorePort
	if root.Process != nil {
		vectorStoreForSearch = root.Process.VectorSvc
	}

	var embeddingReg search.EmbeddingChannelRegistry
	if root.AI != nil {
		embedClient := root.AI.OllamaEmbedClient
		if embedClient == nil {
			embedClient = root.AI.OllamaClient
		}
		if embedClient != nil {
			ollamaEmb := embeddings.NewOllamaEmbedderAdapter(embedClient)
			embeddingReg = newEmbeddingRegistryAdapter(qdrantsearch.NewTextEmbedderAdapter(ollamaEmb), nil)
		}
	}

	var mediaRepo search.MediaReadRepository
	if root.Repos != nil {
		mediaRepo = newSearchReadAdapter(root.Repos.ClipsRepo)
	}

	var deliveryPort search.AssetDeliveryService
	if cfg != nil && cfg.Security.DeliveryHMACSecret != "" {
		baseURL := cfg.External.VeloxBaseURL
		if baseURL == "" {
			baseURL = fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
		}
		signer, err := delivery.NewSigner(
			[]byte(cfg.Security.DeliveryHMACSecret),
			0,
			baseURL,
			"/api/internal/v1/deliver",
		)
		if err != nil {
			log.Warn("registerInternalModules: delivery signer construction failed; semantic backend delivery disabled",
				zap.Error(err))
		} else {
			deliveryPort = signer
		}
	}

	var rerankerPort rerankerClient
	if root.AI != nil && root.AI.Reranker != nil {
		rerankerPort = root.AI.Reranker
	}

	// Bootstrap all provider adapters before composing the search graph.
	if err := registerArtlist(ctx, registry, log, cfg, root, regWiring); err != nil {
		return registryCrossStepState{}, err
	}

	var providerEntries []TrackedProviderEntry
	if regWiring.ArtlistSvc != nil && regWiring.ArtlistSvc.Service != nil {
		providerEntries = append(providerEntries, TrackedProviderEntry{
			Id: "artlist", Kind: ProviderKindSearch,
			Search: artlistadapter.NewGatewayAdapter(regWiring.ArtlistSvc.Service),
		})
	}
	if cfg.Features.YouTubeEnabled && root.Domains != nil && root.Domains.YoutubeClipService != nil {
		providerEntries = append(providerEntries, TrackedProviderEntry{
			Id: "youtube", Kind: ProviderKindSearch,
			Search: youtubeadapter.NewAdapter(root.Domains.YoutubeClipService),
		})
	}
	if root.Domains != nil && root.Domains.ImageSearchResolver != nil {
		providerEntries = append(providerEntries, TrackedProviderEntry{
			Id: "image", Kind: ProviderKindSearch,
			Search: appimages.NewResolverSearchProvider(root.Domains.ImageSearchResolver),
		})
	}
	regWiring.StockPipeline = nil
	stockW, stockErr := WireStockPipeline(cfg, log, root)
	if stockErr == nil && stockW != nil && stockW.Module != nil {
		regWiring.StockPipeline = stockW
		if err := tryRegisterModuleStrict(registry, log, stockW.Module, WithRegistrationPoint("register.StockPipeline")); err != nil {
			return registryCrossStepState{}, err
		}
		if stockW.BatchModule != nil {
			if err := tryRegisterModuleStrict(registry, log, stockW.BatchModule, WithRegistrationPoint("register.StockBatches")); err != nil {
				return registryCrossStepState{}, err
			}
		}
		if stockW.Service != nil && root.Jobs != nil && root.Jobs.Service != nil {
			if err := stockW.Service.RegisterHandler(root.Jobs.Service); err != nil {
				return registryCrossStepState{}, err
			}
		}
		providerEntries = append(providerEntries, TrackedProviderEntry{Id: "stock", Kind: ProviderKindFetch, Fetch: stockadapter.NewAdapter(stockW.Service)})
	}
	scriptAssetsDescriptor, err := scriptassets.Build(scriptassets.Dependencies{Logger: log})
	if err != nil {
		return registryCrossStepState{}, err
	}
	providerDescriptor, ok := scriptAssetsDescriptor.(module.DescriptorProviders)
	if !ok {
		return registryCrossStepState{}, fmt.Errorf("script-assets descriptor does not implement api.DescriptorProviders")
	}
	if err := bootstrapProviderRegistry(providerReg, providerEntries, []module.DescriptorProviders{providerDescriptor}); err != nil {
		return registryCrossStepState{}, err
	}
	searchFanOut, searchBackends, searchAgg := registerSearchBackend(
		log,
		providerReg,
		root.Repos.ClipsRepo,
		embeddingReg,
		vectorStoreForSearch,
		mediaRepo,
		deliveryPort,
		rerankerPort,
	)
	crossStep := registryCrossStepState{
		SearchFanOut:       searchFanOut,
		SearchBackends:     searchBackends,
		SearchAggregator:   searchAgg,
		ScriptAssetsModule: scriptAssetsDescriptor,
		IdempotencyHandler: idemHandler,
	}

	// Fase 4.1: native Pexels image search provider. Registered
	// alongside Artlist + YouTube so the canonical SearchFanOut
	if err := registerYouTubeClip(registry, log, cfg, root, regWiring, searchAgg, searchFanOut, idemHandler); err != nil {
		return registryCrossStepState{}, err
	}

	mediaIngestW, mediaIngestErr := WireMediaIngest(cfg, log, &MediaIngestBundle{
		DB:                root.DB,
		Assets:            root.Repos.Assets,
		DriveUploader:     root.Drive.DriveUploader,
		Lifecycle:         root.Drive.Lifecycle,
		Publisher:         root.Drive.Publisher,
		ImageRepo:         root.Repos.ImageRepo,
		VoiceoverRepo:     root.Repos.VoiceoverRepo,
		ClipsRepo:         root.Repos.ClipsRepo,
		AssetIndexService: root.Search.AssetIndexService,
		PrebuiltService:   root.Domains.IngestService,
		Dispatcher:        root.Outbox.Dispatcher,
	}, idemHandler)
	regWiring.MediaIngest = mediaIngestW
	if mediaIngestErr != nil {
		log.Warn("failed to wire module", zap.String("module", "MediaIngest"), zap.Error(mediaIngestErr))
	} else if mediaIngestW != nil && mediaIngestW.Module != nil {
		if err := tryRegisterModuleStrict(registry, log, mediaIngestW.Module, WithRegistrationPoint("register.MediaIngest")); err != nil {
			return registryCrossStepState{}, fmt.Errorf("wire registry: media-ingest: %w", err)
		}
	}

	scraperHandler := assetsapi.NewScraperHandler(cfg.External.NodeScraperDir, processRunnerAdapter)
	scraperMod := module.NewRouteModule(
		"scraper",
		func() bool { return scraperHandler != nil },
		"/scraper",
		scraperHandler,
		log,
	)
	log.Info("created Scraper module")
	if err := tryRegisterModuleStrict(registry, log, scraperMod, WithRegistrationPoint("register.Scraper")); err != nil {
		return registryCrossStepState{}, fmt.Errorf("wire registry: scraper: %w", err)
	}

	var imagesDir string
	if cfg != nil {
		imagesDir = cfg.Storage.ImagesPath()
	}
	fullImgBundle := &FullImagesBundle{
		ImageService: root.Domains.ImageService,
		ImagesDir:    imagesDir,
	}
	fullW, fullErr := WireFullImages(fullImgBundle, cfg, log)
	if fullErr != nil {
		log.Warn("registerInternalModules Step 7 WireFullImages failed (godlike/07 fail-closed)", zap.Error(fullErr))
		regWiring.FullImages = nil
	} else if fullW != nil && fullW.Module != nil {
		regWiring.FullImages = fullW
		if err := tryRegisterModuleStrict(registry, log, fullW.Module, WithRegistrationPoint("register.FullImages")); err != nil {
			return registryCrossStepState{}, fmt.Errorf("wire registry: full-images: %w", err)
		}
		log.Info("registerInternalModules Step 7 FullImages pipeline mounted")
	} else {
		regWiring.FullImages = nil
	}

	return crossStep, nil
}

func registerArtlist(ctx context.Context, registry *module.Registry, log *zap.Logger, cfg *config.Config, root *wiring.ComposeRoot, regWiring *RegistryWiring) error {
	if !cfg.Features.ArtlistEnabled {
		log.Info("registerArtlist: feature disabled (cfg.Features.ArtlistEnabled=false); skipping route registration")
		regWiring.ArtlistSvc = nil
		return nil
	}

	artlistWiring, err := WireArtlist(
		ctx,
		log,
		cfg,
		&wiring.ArtlistBundle{
			MediaExec:          root.MediaExec,
			Committer:          sqassets.NewSQLiteAssetCommitter(root.DB.DB, root.Outbox.EventsRepo, log),
			DB:                 root.DB,
			Assets:             root.Repos.Assets,
			ClipsRepo:          root.Repos.ClipsRepo,
			DriveClient:        nil,
			DriveUploader:      root.Drive.DriveUploader,
			Publisher:          root.Drive.Publisher,
			AssetIndexService:  root.Search.AssetIndexService,
			ClipIndexerService: root.Process.ClipIndexerService,
			MediaProcessor:     root.Process.MediaProcessor,
			Jobs:               root.Jobs,
			CatalogSyncService: root.Sync.CatalogSync,
			TextTrackRepo:      root.Repos.TextTrackRepo,
		},
		root.Outbox.Dispatcher,
		root.Drive.Reader,
		root.Drive.Lifecycle,
		root.Domains.MetaWriter,
		root.Drive.DestResolver,
		root.TextTracks.FanOut,
	)
	if err != nil {
		var depMissing ErrArtlistDepMissing
		if errors.As(err, &depMissing) {
			log.Error("registerArtlist: mandatory dependency strictly required when Artlist is enabled; aborting boot (godlike/07 fail-closed)",
				zap.String("root_path", "/api/artlist/*"),
				zap.String("missing_dep", depMissing.Kind.String()),
				zap.String("missing_field", depMissing.Field),
				zap.Error(err),
			)
		} else {
			log.Error("registerArtlist: WireArtlist unexpected failure; aborting boot (godlike/07 fail-closed)",
				zap.String("root_path", "/api/artlist/*"),
				zap.Error(err),
			)
		}
		return fmt.Errorf("registerArtlist aborting boot (godlike/07 fail-closed): %w", err)
	}

	if err := tryRegisterModuleStrict(registry, log, artlistWiring.Module, WithRegistrationPoint("register.Artlist")); err != nil {
		_ = artlistWiring.Service.Close()
		return fmt.Errorf("registerArtlist: tryRegisterModuleStrict: %w", err)
	}

	regWiring.ArtlistSvc = artlistWiring
	if err := WireArtlistJobBindings(artlistWiring.Service, root.Jobs); err != nil {
		_ = artlistWiring.Service.Close()
		return fmt.Errorf("wire registry: artlist: %w", err)
	}

	log.Info("registerArtlist: ART-001 reversal milestone complete",
		zap.String("descriptor_module_name", artlistWiring.Module.Name()),
	)
	return nil
}

func registerYouTubeClip(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *wiring.ComposeRoot, regWiring *RegistryWiring, searchSvc *search.Aggregator, searchFanOut search.SearchFanOut, idempotencyHandler gin.HandlerFunc) error {
	if !cfg.Features.YouTubeEnabled {
		log.Info("registerYouTubeClip: YouTube feature is disabled; skipping HTTP route registration")
		regWiring.YouTubeClip = nil
		return nil
	}

	descriptor, err := youtubeapi.Build(youtubeapi.Dependencies{
		Core: youtubeapi.CoreDeps{
			Service:       root.Domains.YoutubeClipService,
			Jobs:          root.Jobs.Facade,
			ToolChecker:   toolCheckerAdapter,
			ClipStorePort: nil,
			StockService:  root.Domains.YoutubeClipService.StockService(),
		},
		Search: youtubeapi.SearchDeps{
			Service: searchSvc,
			FanOut:  searchFanOut,
		},
		Transport: youtubeapi.TransportDeps{
			Idempotency: idempotencyHandler,
			EnabledFunc: func() bool { return cfg.Features.YouTubeEnabled },
			ModuleOpts:  nil,
		},
		Observability: youtubeapi.ObservabilityDeps{
			Logger: log,
		},
	})
	if err != nil {
		return fmt.Errorf("registerYouTubeClip: youtube.Build: %w", err)
	}
	yd, ok := descriptor.(*youtubeapi.YouTubeDescriptor)
	if !ok || yd == nil {
		return fmt.Errorf("registerYouTubeClip: youtube.Build returned unexpected descriptor type %T (want *youtubeapi.YouTubeDescriptor)", descriptor)
	}
	regWiring.YouTubeClip = &wiring.YouTubeClipWiring{
		Module:  yd.Module,
		Service: yd.Service,
	}
	log.Info("created YouTubeClip module via youtube.Build (Blocco C1-Step 4)")
	return tryRegisterModuleStrict(registry, log, yd, WithRegistrationPoint("register.YouTubeClip"))
}

func registerJobsRoute(registry *module.Registry, log *zap.Logger, root *wiring.ComposeRoot) error {
	capability := capjobs.NewBundle(
		root.Jobs.Service,
		root.Jobs.Service,
		func() bool { return true },
		log,
	)
	if err := registry.RegisterCapabilityModule(capability, module.BuildContext{}); err != nil {
		return fmt.Errorf("wire registry: jobs: %w", err)
	}
	log.Info("created Jobs module")
	return nil
}
