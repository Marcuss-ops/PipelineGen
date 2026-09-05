package wiring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	searchwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/search"
	youtubewiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/youtube"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	artlistadapter "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	stockadapter "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock"
	youtubeadapter "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/youtube"
	scriptassetsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/scriptassets"
	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	youtubeapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/youtube"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	clipadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/adapters"
	appimages "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	capjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediasearch"
	outboxapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

func registerInternalModules(ctx context.Context, registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, regWiring *RegistryWiring) (registryCrossStepState, error) {
	idemPlus := middleware.NewIdempotency(root.Repos.IdempotencyStore, log)
	idemHandler := idemPlus.Handler()

	var providerReg *providers.Registry
	if root.Search != nil {
		providerReg = root.Search.ProviderRegistry
	}

	// POSTGRES-MEDIA-CUTOVER: resolve BOTH semantic retrieval and hydration
	// from the same canonical PostgreSQL MediaSearcher. There is deliberately
	// no seed from root.Process.VectorSvc and no SQLite hydration adapter, so
	// Qdrant/SQLite cannot re-enter the media read path as fallbacks.
	vectorStoreForSearch, mediaRepo, mediaSearchSelected, mediaSearchErr := searchwiring.SelectMediaSearchStore(cfg, root.MediaPostgres, log)
	if mediaSearchErr != nil {
		return registryCrossStepState{}, mediaSearchErr
	}
	if !mediaSearchSelected {
		log.Info("registerInternalModules: media PostgreSQL disabled; semantic media backend not deployed")
	}

	var embeddingReg search.EmbeddingChannelRegistry
	if cfg != nil && cfg.ClipIndexer.ServerURL != "" {
		// Catalog query vectors must come from the same E5 sidecar contract
		// as indexed document vectors. Ollama is a chat/legacy embedder and
		// must not silently create a second vector space.
		textEmb := embeddings.NewHTTPTextEmbedder(cfg.ClipIndexer.ServerURL)
		embeddingReg = newEmbeddingRegistryAdapter(qdrantsearch.NewTextEmbedderAdapter(textEmb), nil)
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
			deliveryPort = &deliverySignerAdapter{signer: signer}
		}
	}

	var rerankerPort searchwiring.RerankerClient
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
		providerEntries = append(providerEntries, TrackedProviderEntry{Id: "stock", Kind: ProviderKindSearch, Search: stockadapter.NewAdapter(stockW.Service)})
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

	searchFanOut, searchBackends, searchAgg, searchErr := searchwiring.Build(
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

	mediaIngestCommitter, mediaIngestErr2 := newCanonicalAssetCommitter(root.MediaPostgres, log)
	if mediaIngestErr2 != nil {
		return registryCrossStepState{}, fmt.Errorf("wire registry: media ingest committer: %w", mediaIngestErr2)
	}
	if mediaIngestCommitter == nil {
		log.Warn("wire registry: media PostgreSQL unavailable — MediaIngest module skipped (media plane not deployed)")
	}

	mediaIngestW, mediaIngestErr := WireMediaIngest(cfg, log, &MediaIngestBundle{
		DB:                root.DB,
		CacheDB:           root.CacheDB,
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
		Committer:         mediaIngestCommitter,
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

func registerArtlist(ctx context.Context, registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, regWiring *RegistryWiring) error {
	if !cfg.Features.ArtlistEnabled {
		log.Info("registerArtlist: feature disabled (cfg.Features.ArtlistEnabled=false); skipping route registration")
		regWiring.ArtlistSvc = nil
		return nil
	}

	artlistWiring, err := WireArtlist(
		ctx,
		log,
		cfg,
		&ArtlistBundle{
			MediaExec:          root.MediaExec,
			Committer:          canonicalCommitterOrSkipped(root, log),
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

func registerYouTubeClip(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, regWiring *RegistryWiring, searchSvc *search.Aggregator, searchFanOut search.SearchFanOut, idempotencyHandler gin.HandlerFunc) error {
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
	regWiring.YouTubeClip = &youtubewiring.YouTubeClipWiring{
		Module:  yd.Module,
		Service: yd.Service,
	}
	log.Info("created YouTubeClip module via youtube.Build (Blocco C1-Step 4)")
	return tryRegisterModuleStrict(registry, log, yd, WithRegistrationPoint("register.YouTubeClip"))
}

func registerClipRender(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, idempotencyHandler gin.HandlerFunc) error {
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
	resolver, err := clipadapters.NewClipRenderAssetResolver(root.Repos.Assets, log)
	if err != nil {
		return fmt.Errorf("registerClipRender: build asset resolver: %w", err)
	}
	var driveReader drivepkg.Reader
	if root.Drive != nil {
		driveReader = root.Drive.Reader
	}
	materializer, err := clipadapters.NewClipRenderMaterializer(driveReader, filepath.Join(cfg.Storage.TempPath(), "cliprender"), log)
	if err != nil {
		return fmt.Errorf("registerClipRender: build asset materializer: %w", err)
	}
	preparedResolver, err := cliprender.NewPreparedAssetResolver(filepath.Join(cfg.Storage.TempPath(), "cliprender", "assets"), materializer)
	if err != nil {
		return fmt.Errorf("registerClipRender: build prepared asset resolver: %w", err)
	}
	materializerPort := cliprender.AssetMaterializer(preparedResolver)
	transcriptResolver := clipadapters.NewClipRenderTranscriptResolver(log)
	if root.Repos != nil {
		transcriptResolver.SetRepo(root.Repos.TextTrackRepo)
	}
	if root.TextTracks != nil {
		transcriptResolver.SetAcquire(root.TextTracks.AcquireService)
	}
	if root.Domains != nil {
		transcriptResolver.SetCueWriter(root.Domains.CueWriter)
	}
	// Streaming PCM transcriber (spec §4: zero temp WAV). Construction is
	// fail-closed with a typed error when python3/ffmpeg/bridge are missing;
	// the resolver falls back to the canonical WAV-chain with a warning.
	if streaming, err := clipadapters.NewClipRenderStreamingTranscriber(cfg, log); err != nil {
		log.Warn("registerClipRender: streaming transcriber unavailable; transcript generation will use the WAV chain",
			zap.String("reason", err.Error()))
	} else {
		transcriptResolver.SetStreaming(streaming)
	}

	preparer, err := cliprender.NewPreparer(
		resolver,
		materializerPort,
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
	subtitleCompiler := clipadapters.NewClipRenderSubtitleCompiler()
	if root.Repos != nil {
		subtitleCompiler.SetArtifactRepository(root.Repos.SubtitleArtifactRepo)
	}
	worker.WithSubtitleCompiler(subtitleCompiler)

	// RenderingGen/Chronon render boundary: the shared executor owns queue
	// submission; the remote worker owns Chronon execution.
	// config (encoder policy + profile owned by the composition root, never
	// by Rust). Fail-closed when the media config is missing, mirroring
	// WireStockPipeline. The Chronon clip executor is attached to the worker via the
	// RenderExecutor port; the render phase consumes it in the follow-up
	// step (until then Handle fails closed with ErrRenderPhaseNotImplemented).
	mediaConfig := root.MediaExec
	if mediaConfig == (mediaexec.ExecutionConfig{}) {
		return fmt.Errorf("registerClipRender: resolved media execution config is required when ClipRenderEnabled=true (root.MediaExec)")
	}
	renderRuntime, runtimeErr := BuildClipRenderRuntime(cfg, root, log)
	if runtimeErr != nil {
		return fmt.Errorf("registerClipRender: build shared render runtime: %w", runtimeErr)
	}
	worker.WithRenderExecutor(renderRuntime.RenderingGenExecutor)
	if root.Drive == nil || root.Drive.Publisher == nil || root.DB == nil || root.Outbox == nil || root.Outbox.EventsRepo == nil {
		return fmt.Errorf("registerClipRender: Drive publisher, SQLite DB and outbox are required for rendered asset publication")
	}
	var committer assetspersistence.AssetCommitter
	{
		w, werr := newCanonicalAssetCommitter(root.MediaPostgres, log)
		if werr != nil {
			return fmt.Errorf("registerClipRender: canonical media writer: %w", werr)
		}
		if w == nil {
			return fmt.Errorf("registerClipRender: canonical media writer unavailable (media PostgreSQL not deployed)")
		}
		committer = w
	}
	publisher, publisherErr := clipadapters.NewClipRenderPublisher(root.Drive.Publisher, committer, log)
	if publisherErr != nil {
		return fmt.Errorf("registerClipRender: build clip render publisher: %w", publisherErr)
	}
	publisher.SetSubtitleArtifactRepository(root.Repos.SubtitleArtifactRepo)
	worker.WithRenderPublisher(publisher)
	// Script/batch leaf-folder resolution (create-or-reuse under the caller's
	// root): routed through the SAME delivery.Publisher, so folder creation
	// has one canonical owner and the ClipRenderPublisher stays dumb (it
	// receives the fully-resolved leaf folder ID and never creates folders).
	destinationResolver, destinationResolverErr := clipadapters.NewClipRenderDestinationFolderResolver(root.Drive.Publisher, log)
	if destinationResolverErr != nil {
		return fmt.Errorf("registerClipRender: build clip render destination resolver: %w", destinationResolverErr)
	}
	worker.WithDestinationFolderResolver(destinationResolver)

	// Overlay compositing hop (entity overlays): the segment resolver reads
	// the SAME content cache the overlay.render handler writes (the
	// RENDERINGGEN_CACHE_ROOT / default root BuildRenderingRuntime uses — a
	// plain directory, so a second cache handle is harmless), and the ffmpeg
	// compositor blends the segment onto the source at the declared window
	// using the resolved encoder policy. Fail-closed at call time: an
	// overlay declared without these adapters is a typed worker error.
	cacheRoot := os.Getenv("RENDERINGGEN_CACHE_ROOT")
	if cacheRoot == "" {
		cacheRoot = filepath.Join(os.TempDir(), "pipelinegen", "renderinggen", "cache")
	}
	overlayCache, cacheErr := infraoverlays.NewCache(cacheRoot)
	if cacheErr != nil {
		return fmt.Errorf("registerClipRender: build overlay cache: %w", cacheErr)
	}
	worker.WithOverlaySegmentResolver(clipadapters.NewOverlaySegmentResolver(overlayCache))
	worker.WithOverlayCompositor(clipadapters.NewFFmpegOverlayCompositor(
		cfg.External.FfmpegPath,
		mediaConfig.Policy.Codec,
		mediaConfig.Policy.Preset,
		mediaConfig.Policy.CRF,
	))
	log.Info("registerClipRender: clip render boundary wired (RenderingGen queue → Chronon certified artifact)",
		zap.String("renderinggen_queue", cfg.External.RenderingGenQueueURL),
		zap.String("encoder", mediaConfig.Policy.Codec),
		zap.String("preset", mediaConfig.Policy.Preset),
		zap.Int("crf", mediaConfig.Policy.CRF),
		zap.Int("profile_width", mediaConfig.Profile.Width),
		zap.Int("profile_height", mediaConfig.Profile.Height),
		zap.Int("profile_fps_num", mediaConfig.Profile.FPSNum),
		zap.Int("profile_fps_den", mediaConfig.Profile.FPSDen),
	)

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

	// Canonical worker binding: parallel preparation and the Chronon-backed
	// RenderingGen execution are real; missing execution wiring fails closed.
	if err := root.Jobs.Facade.RegisterHandler(cliprender.TypeClipRender, appjobs.HandlerFunc(worker.Handle)); err != nil {
		return fmt.Errorf("registerClipRender: bind clip.render handler: %w", err)
	}

	log.Info("created ClipRender module via cliprender.Build (canonical clip post-processing, parallel preparation wired)")
	return tryRegisterModuleStrict(registry, log, descriptor, WithRegistrationPoint("register.ClipRender"))
}

func registerJobsRoute(registry *module.Registry, log *zap.Logger, root *ComposeRoot, wiring *RegistryWiring) error {
	bundle := capjobs.NewBundleWithHistory(
		root.Jobs.Service,
		root.Jobs.Service,
		root.Jobs.History,
		func() bool { return true },
		log,
	)
	if err := registry.RegisterCapabilityModule(bundle, module.BuildContext{}); err != nil {
		return fmt.Errorf("wire registry: jobs: %w", err)
	}
	log.Info("created Jobs module")

	// PG-M2M (Aug 2026): build the M2M job surface from the SAME bundle
	// so Enqueue/Get stay single-implementation. The M2M module is
	// NOT registered in the public /api registry (it would collide
	// with the admin /jobs prefix and inherit the admin Auth guard);
	// it is plumbed through RegistryWiring → AppDeps.Handlers and
	// mounted on its own /api/v1/jobs group by the server composition.
	// Enabled closure is true so the M2M surface mounts whenever the
	// M2MSecurityPort is wired (the port's EnableM2M() is the real
	// gate inside JobClientAuthMiddleware; this closure only decides
	// whether the routes exist at all).
	m2mModule := capjobs.NewM2MJobsModule(bundle.Handler(), func() bool { return true })
	if wiring != nil {
		wiring.M2MJobsHandler = m2mModule
	}
	log.Info("created M2M Jobs module (POST + GET /:id on /api/v1/jobs)")
	return nil
}

// applyLateBindings is retained as an orchestration name for a pure handler
// preparation phase. Provider adapters and descriptor-owned providers have
// already been registered and frozen before this function is called.
func applyLateBindings(_ *module.Registry, log *zap.Logger, root *ComposeRoot, regWiring *RegistryWiring, crossStep registryCrossStepState) (PreparedCapabilities, error) {
	prepared := PreparedCapabilities{}
	if root.Outbox != nil && root.Outbox.EventsRepo != nil {
		regWiring.OutboxHandler = outboxapi.NewHandler(newOutboxMonitorAdapter(root.Outbox.EventsRepo), log)
	}
	// Media-search transport follows the canonical media plane, not the legacy
	// Qdrant process bundle. When PostgreSQL is deployed, readiness reports any
	// missing semantic dependency through the handler instead of hiding the route.
	if root != nil && root.MediaPostgres != nil && crossStep.SearchAggregator != nil {
		searchAgg := mediasearchapi.AggregatorSearcher(crossStep.SearchAggregator)
		regWiring.MediasearchHandler = mediasearchapi.NewHandler(mediasearchapi.WireParams{
			Aggregator: searchAgg, SemanticReady: WireMediasearchReadiness(root, searchAgg), Log: log,
		})
	}
	return prepared, nil
}
