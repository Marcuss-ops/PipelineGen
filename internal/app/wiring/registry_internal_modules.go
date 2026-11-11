package wiring

import (
	"context"
	"database/sql"
	"fmt"

	searchwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/search"
	stockwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/stock"
	youtubewiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
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
import (
	mediamemoryapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)
import (
	wiringlifecycle "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/lifecycle"
	capjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediasearch"
	outboxapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/outbox"
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

	var providerEntries []TrackedProviderEntry
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
		crossStep.StockPrefetcher = stockwiring.NewScriptPrefetcher(stockW.Service, log)
	}

	// Fase 4.1: native Pexels image search provider. Registered
	if err := registerYouTubeClip(registry, log, cfg, root, regWiring, searchAgg, searchFanOut, idemHandler); err != nil {
		return registryCrossStepState{}, err
	}

	// Clip render (canonical VeloxEditing-compatible clip post-processing).
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

	// Step 7 (FullImages) retired (IMAGES-LEGACY-CLEANUP, August 2026):
	// POST /api/fullimages/image/generate was merged into
	// POST /api/images/batch-generate mode=sections; the dedicated
	// fullimages module + wiring were removed.

	return crossStep, nil
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

// ── Step 5c helper ────────────────────────────────────────────────

// registerMediaMemory wires the canonical MediaMemory resolve +
// bindings + feedback surface. The resolver is backed by the Brain
// (canonical 9-level cascade); the binding service is backed by
// SQLite + outbox dispatcher.
func registerMediaMemory(registry *module.Registry, log *zap.Logger, root *ComposeRoot, searchFanOut search.SearchFanOut) error {
	if searchFanOut == nil {
		log.Warn("registerMediaMemory: searchFanOut not wired; skipping (Level 3-9 cascade unavailable)")
		return nil
	}
	if root.DB == nil {
		log.Warn("registerMediaMemory: DB not available; skipping")
		return nil
	}

	resolver, err := WireMediaMemoryResolver(searchFanOut, root.DB.DB, log)
	if err != nil {
		return fmt.Errorf("registerMediaMemory: wire resolver: %w", err)
	}

	txMgr := outbox.NewManager(root.DB.DB, log)
	eventsRepo := outboxevents.NewRepository(root.DB.DB)
	bindingSvc := WireBindingService(root.DB.DB, txMgr, eventsRepo, log)

	handler := mediamemoryapi.NewHandler(mediamemoryapi.WireParams{
		Resolver:       resolver,
		PolicyResolver: mediamemoryapi.NewResolutionPolicyResolver(),
		Bindings:       bindingSvc,
		Log:            log,
	})

	descriptor := mediamemoryapi.Build(handler, log)
	if err := tryRegisterModuleStrict(registry, log, descriptor, WithRegistrationPoint("register.MediaMemory")); err != nil {
		return fmt.Errorf("registerMediaMemory: tryRegisterModuleStrict: %w", err)
	}

	log.Info("registerMediaMemory: mediamemory module mounted")
	return nil
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
		regWiring.OutboxHandler = outboxapi.NewHandler(wiringlifecycle.NewOutboxMonitorAdapter(root.Outbox.EventsRepo), log)
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
