package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

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
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	scriptassetsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/scriptassets"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	appimages "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	capjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/delivery"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
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
	if cfg != nil && cfg.ClipIndexer.ServerURL != "" {
		// Catalog query vectors must come from the same E5 sidecar contract
		// as indexed document vectors. Ollama is a chat/legacy embedder and
		// must not silently create a second vector space.
		textEmb := embeddings.NewHTTPTextEmbedder(cfg.ClipIndexer.ServerURL)
		embeddingReg = newEmbeddingRegistryAdapter(qdrantsearch.NewTextEmbedderAdapter(textEmb), nil)
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
	scriptAssetsDescriptor, err := scriptassetsapi.Build(scriptassetsapi.Dependencies{Logger: log})
	if err != nil {
		return registryCrossStepState{}, err
	}
	if err := bootstrapProviderRegistry(providerReg, providerEntries, []module.DescriptorProviders{scriptAssetsDescriptor}); err != nil {
		return registryCrossStepState{}, err
	}
	// PR-SEARCH-UNIVERSE (August 2026): the provider backend resolves
	// source_type|source_ref → canonical asset via the canonical identity
	// resolver instead of fabricating an AssetID from the provider ID.
	var canonicalResolver search.CanonicalIdentityResolver
	if root != nil {
		var db *sql.DB
		if root.DB != nil {
			db = root.DB.DB
		}
		canonicalResolver = newCanonicalIdentityResolver(db)
	}

	searchFanOut, searchBackends, searchAgg, searchErr := registerSearchBackend(
		log,
		providerReg,
		root.Repos.ClipsRepo,
		embeddingReg,
		vectorStoreForSearch,
		mediaRepo,
		deliveryPort,
		rerankerPort,
		canonicalResolver,
	)
	if searchErr != nil {
		return registryCrossStepState{}, searchErr
	}
	crossStep := registryCrossStepState{
		SearchFanOut:       searchFanOut,
		SearchBackends:     searchBackends,
		SearchAggregator:   searchAgg,
		IdempotencyHandler: idemHandler,
	}

	// Fase 4.1: native Pexels image search provider. Registered
	// alongside Artlist + YouTube so the canonical SearchFanOut
	if err := registerYouTubeClip(registry, log, cfg, root, regWiring, searchAgg, searchFanOut, idemHandler); err != nil {
		return registryCrossStepState{}, err
	}

	// Clip render (canonical VeloxEditing-compatible clip
	// post-processing): a NEW capability on the same Master queue —
	// no second renderer, no second queue.
	if err := registerClipRender(registry, log, cfg, root, idemHandler); err != nil {
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

	// Step 7 (FullImages) retired (IMAGES-LEGACY-CLEANUP, August 2026):
	// POST /api/fullimages/image/generate was merged into
	// POST /api/images/batch-generate mode=sections; the dedicated
	// fullimages module + wiring were removed.

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
			Committer:          newCanonicalAssetCommitter(root.DB.DB, root.Outbox.EventsRepo, log),
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

func registerClipRender(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *wiring.ComposeRoot, idempotencyHandler gin.HandlerFunc) error {
	if !cfg.Features.ClipRenderEnabled {
		log.Info("registerClipRender: ClipRender feature is disabled; skipping HTTP route registration + job binding")
		return nil
	}
	if root.Jobs == nil || root.Jobs.Facade == nil {
		return fmt.Errorf("registerClipRender: root.Jobs.Facade is required when ClipRenderEnabled=true (the POST /clips/render enqueue path needs the Master job service)")
	}

	// Parallel-preparation adapters (composition root owns mechanics,
	// the capability owns the ports). Every adapter is fail-closed at
	// call time when a dependency is missing.
	resolver := &clipRenderAssetResolver{assets: root.Repos.Assets}
	var driveReader drivepkg.Reader
	if root.Drive != nil {
		driveReader = root.Drive.Reader
	}
	materializer := &clipRenderMaterializer{
		drive:      driveReader,
		scratchDir: filepath.Join(cfg.Storage.TempPath(), "cliprender"),
	}
	transcriptResolver := &clipRenderTranscriptResolver{log: log}
	if root.Repos != nil {
		transcriptResolver.repo = root.Repos.TextTrackRepo
	}
	if root.TextTracks != nil {
		transcriptResolver.acquire = root.TextTracks.AcquireService
	}
	if root.Domains != nil {
		transcriptResolver.cueWriter = root.Domains.CueWriter
	}
	// Streaming PCM transcriber (spec §4: zero temp WAV). Construction is
	// fail-closed with a typed error when python3/ffmpeg/bridge are missing;
	// the resolver falls back to the canonical WAV-chain with a warning.
	if streaming, err := newClipRenderStreamingTranscriber(cfg, log); err != nil {
		log.Warn("registerClipRender: streaming transcriber unavailable; transcript generation will use the WAV chain",
			zap.String("reason", err.Error()))
	} else {
		transcriptResolver.streaming = streaming
	}

	preparer, err := cliprender.NewPreparer(
		resolver,
		materializer,
		transcriptResolver,
		cliprender.NewContractResolver(),
		log,
	)
	if err != nil {
		return fmt.Errorf("registerClipRender: build preparer: %w", err)
	}
	worker, err := cliprender.NewWorker(preparer, filepath.Join(cfg.Storage.TempPath(), "cliprender"), log)
	if err != nil {
		return fmt.Errorf("registerClipRender: build worker: %w", err)
	}
	// Deterministic ASS compiler (canonical texttracks content generator —
	// single owner). Subtitles.enabled=true without a wired compiler fails
	// closed in the worker; this wiring makes burn+sidecar always available.
	worker.WithSubtitleCompiler(&clipRenderSubtitleCompiler{})
	// The canonical ASS compiler (reuse of the existing
	// texttracks.SubtitleArtifactMaterializer) is wired by the ASS-compiler
	// step; until then subtitled jobs fail closed with the typed sentinel.

	descriptor, err := cliprender.Build(cliprender.Dependencies{
		Jobs:        root.Jobs.Facade,
		EnabledFunc: func() bool { return cfg.Features.ClipRenderEnabled },
		Idempotency: idempotencyHandler,
		Logger:      log,
		ModuleOpts:  nil,
	})
	if err != nil {
		return fmt.Errorf("registerClipRender: cliprender.Build: %w", err)
	}

	// Canonical worker binding: parallel preparation is real and runs on
	// claimed jobs; the render phase still fails closed with the typed
	// sentinel until the follow-up step lands render_clip.
	if err := root.Jobs.Facade.RegisterHandler(cliprender.TypeClipRender, appjobs.HandlerFunc(worker.Handle)); err != nil {
		return fmt.Errorf("registerClipRender: bind clip.render handler: %w", err)
	}

	log.Info("created ClipRender module via cliprender.Build (canonical clip post-processing, parallel preparation wired)")
	return tryRegisterModuleStrict(registry, log, descriptor, WithRegistrationPoint("register.ClipRender"))
}

func registerJobsRoute(registry *module.Registry, log *zap.Logger, root *wiring.ComposeRoot) error {
	capability := capjobs.NewBundleWithHistory(
		root.Jobs.Service,
		root.Jobs.Service,
		root.Jobs.History,
		func() bool { return true },
		log,
	)
	if err := registry.RegisterCapabilityModule(capability, module.BuildContext{}); err != nil {
		return fmt.Errorf("wire registry: jobs: %w", err)
	}
	log.Info("created Jobs module")
	return nil
}
