package wiring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	searchwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/search"
	youtubewiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	artlistadapter "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	stockadapter "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock"
	youtubeadapter "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/youtube"
	scriptassetsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/scriptassets"
	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	youtubeapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/youtube"
	appimages "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
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
	// no seed from the legacy vector service and no legacy hydration adapter,
	// so Qdrant/SQLite cannot re-enter the media read path as fallbacks.
	// SelectMediaSearchStore(cfg, root.MediaPostgres) is the canonical
	// fail-closed resolver for the media search store; the deleted
	// certify-media-cutover driver's Gate A/C used to probe it, and the live
	// enforcement is now the Go gate suite + the postgres/media tests.
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
		textEmb := embeddings.NewHTTPTextEmbedderWithTimeout(cfg.ClipIndexer.ServerURL, cfg.ClipIndexer.EmbedTimeout())
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
	} else if stockErr != nil {
		// FAIL-LOUD GATING (September 2026): the error used to be captured in
		// stockErr and never read, so a required-dep miss (Drive.Admin is the
		// usual one: the port exists only when the Drive SDK handle could be
		// built from credentials) made /api/stock-pipeline/* and
		// /api/stock-batches/* disappear SILENTLY while the production config
		// advertises stock_pipeline_enabled: true. The only symptom was a
		// diff between the live router and the generated API manifest. The
		// capability stays absent (not mounted is the honest state — no fake
		// availability), but the reason is now observable at boot.
		log.Warn("registerInternalModules: stock pipeline NOT mounted — WireStockPipeline returned an error",
			zap.String("registration_point", "register.StockPipeline"),
			zap.Bool("feature_flag_enabled", cfg != nil && cfg.Features.StockPipelineEnabled),
			zap.Error(stockErr))
	} else if stockW != nil && stockW.Module == nil {
		// Defensive: wiring succeeded but produced no route Module. Same
		// silent-absence class as above (e.g. a future refactor that stops
		// populating Module) — surface it instead of dropping the routes.
		log.Warn("registerInternalModules: stock pipeline wiring produced no route Module; /api/stock-pipeline/* stays unmounted",
			zap.String("registration_point", "register.StockPipeline"))
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
		var legacyDB *sql.DB
		if root.DB != nil {
			legacyDB = root.DB.DB
		}
		// Identity is a MEDIA fact: resolve against the PostgreSQL media SSOT
		// whenever it is deployed, and keep the operational SQLite registry
		// only for the graceful-degrade path (MEDIA-SSOT P1-8).
		canonicalResolver = newCanonicalIdentityResolver(root.MediaPostgres, legacyDB)
	}

	// MEDIA-SSOT P1-6: the local media backend is derived from the canonical
	// PostgreSQL media read repository (mediaRepo) inside Build. The legacy
	// SQLite ClipsRepo is deliberately NOT passed — it must never back a
	// media catalog search again.
	searchFanOut, searchBackends, searchAgg, searchErr := searchwiring.Build(
		log,
		providerReg,
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
	if stockW != nil && stockW.Service != nil {
		crossStep.StockPrefetcher = newStockScriptPrefetcher(stockW.Service, log)
	}

	// Fase 4.1: native Pexels image search provider. Registered
	// alongside Artlist + YouTube so the canonical SearchFanOut
	if err := registerYouTubeClip(registry, log, cfg, root, regWiring, searchAgg, searchFanOut, idemHandler); err != nil {
		return registryCrossStepState{}, err
	}

	// Clip render (canonical VeloxEditing-compatible clip post-processing).
	if err := registerClipRender(registry, log, cfg, root, idemHandler); err != nil {
		return registryCrossStepState{}, err
	}
	// video.create durable workflow parent (POST /api/v1/jobs, no dedicated endpoint).
	if err := registerVideoCreate(root, log, cfg.External.RustMusclesPath, cfg.External.FfmpegPath, searchAgg); err != nil {
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
			MediaDB:            root.MediaPostgres,
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
	// The media domain deliberately degrades when PostgreSQL media is not
	// deployed. In that mode BuildDomainBundle returns an artifact-only bundle
	// without media-dependent services, so an enabled config flag must not turn
	// an otherwise healthy server boot into a nil-service composition failure.
	if root == nil || root.Domains == nil || root.Domains.YoutubeClipService == nil {
		log.Warn("registerYouTubeClip: YouTube feature enabled but media service is unavailable; skipping HTTP route registration")
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
			// T1.3: pre-extraction dedup probe on the media SSOT. Nil when
			// media PostgreSQL is disabled → GET /api/clips/exists answers
			// 503 (fail-closed), never a silent "not registered".
			ExistencePort: newYouTubeClipExistencePort(root.MediaPostgres),
			// Transcript endpoint: re-packages the SAME SubtitleFetcher the
			// acquisition chain uses (VTT-only yt-dlp fetch, canonical
			// ParseVTTFile). Nil when the domain bundle built without it →
			// GET /api/clips/transcript answers 503.
			TranscriptSource: root.Domains.YoutubeClipService.Subtitles(),
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
