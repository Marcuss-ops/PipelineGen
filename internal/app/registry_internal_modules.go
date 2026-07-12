// Package app — internal (bundle-driven) module registrations (PR4 split).
//
// PR4 mechanical split (June 2026): relocated from registry.go without
// signature or behaviour changes. Each function in this file registers
// a bundle-driven module — the construction needs a typed bundle from
// the ComposeRoot + bundle-specific deps from sibling bundles. Public
// route modules (System, Images, ScriptHistory, etc.) live in
// registry_public_modules.go; thin wrappers plus the bulk of the
// wiring complexity live here.
//
// The orchestrator's single call to registerInternalModules wraps all
// sub-registrations (idempotency middleware setup + search fan-out
// + Artlist + YouTubeClip + MediaIngest + Scraper + FullImages +
// StockPipeline) in the canonical DAG order so cross-step state
// (searchFanOut + searchBackends + idempotencyHandler) is populated
// before registerAssets needs it.
package app

import (
	"context"
	"errors"
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	youtubeapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/youtube"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/embeddings"
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// registerInternalModules wraps the bundle-driven registrations into a
// single orchestrator-callable function. The internal order matches the
// pre-PR4 sequence:
//
//  1. Idempotency middleware + search fan-out (cross-step state for Assets)
//  2. Artlist (consumes CatalogSync, AssetIndexService, etc.; root.Outbox.Dispatcher)
//  3. ScriptFlow (extracted from inline block; calls wireScriptFlow)
//  4. YouTubeClip (consumes searchAgg; root.Jobs.Facade)
//  5. MediaIngest (bundle-driven; consuming idemHandler)
//  6. Scraper (infra-only)
//  7. FullImages (bundle-driven; ImageService + MediaStore)
//  8. StockPipeline (bundle-driven)
//
// Returns errors from sub-registrations; caller's strict-path Register
// failures propagate to the orchestrator as fatal.
func registerInternalModules(ctx context.Context, registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, wiring *RegistryWiring) error {
	// Step 1a — Idempotency middleware. Used by YouTubeClip +
	// MediaIngest + Assets (post-idempotency store is shared).
	//
	// PR 8 (June 2026): IdempotencyPlus constructs the canonical reusable
	// Gin idempotency middleware instance from RepoBundle.IdempotencyStore;
	// shared across YouTubeClip, MediaIngest, and (via AssetsBundle) clips
	// + register handlers.
	idemPlus := middleware.NewIdempotency(root.Repos.IdempotencyStore, log)
	idemHandler := idemPlus.Handler()

	// Step 1b — Search fan-out. Constructed once and shared between
	// YouTubeClip + Assets. The pre-PR-2 parallel
	// `providers.NewSearchAggregator` is gone (git-rm'd); the
	// canonical *search.Aggregator lives behind the SearchFanOut
	// decorator which exposes the user-spec Option{Hits, Latencies}
	// Stats surface. Composition-stable failure mode: a
	// BuildCanonicalSearchFanOut error aborts boot so a
	// misconfigured backend set is visible at startup rather than
	// silently degrading to partial coverage.
	var providerReg *providers.Registry
	if root.Search != nil {
		providerReg = root.Search.ProviderRegistry
	}
	// Fase 6 (July 2026): wire the canonical 4-port semantic backend.
	// VectorStore (Qdrant SearchAdapter) from Process bundle,
	// EmbeddingChannelRegistry from Ollama (OllamaClient →
	// ollamaEmbedder → qdrant.TextEmbedder → embeddingRegistryAdapter
	// → search.EmbeddingChannelRegistry), MediaReadRepository from
	// ClipsRepository, and Delivery from the HMAC Signer
	// (cfg.Security.DeliveryHMACSecret ≥ 32 bytes). When any of the
	// four ports is nil, BuildSearchBackends silently skips the
	// semantic backend (graceful degradation to providers + local).
	var vectorStoreForSearch assetsearch.VectorStorePort
	if root.Process != nil {
		vectorStoreForSearch = root.Process.VectorSvc
	}

	// EmbeddingChannelRegistry: Ollama embed client → ollamaEmbedder → qdrant.TextEmbedder → embeddingRegistryAdapter.
	// Uses a dedicated embedding client (OllamaEmbedClient, model=nomic-embed-text) so chat and embedding
	// can use different Ollama models — Ollama returns 500 when a chat model is sent to /api/embeddings.
	var embeddingReg search.EmbeddingChannelRegistry
	if root.AI != nil {
		embedClient := root.AI.OllamaEmbedClient
		if embedClient == nil {
			embedClient = root.AI.OllamaClient // backward compat: fall back to chat client
		}
		if embedClient != nil {
			ollamaEmb := embeddings.NewOllamaEmbedderAdapter(embedClient)
			embeddingReg = newEmbeddingRegistryAdapter(qdrantsearch.NewTextEmbedderAdapter(ollamaEmb), nil)
		}
	}

	// MediaReadRepository: ClipsRepository → search.MediaReadRepository
	var mediaRepo search.MediaReadRepository
	if root.Repos != nil {
		mediaRepo = newSearchReadAdapter(root.Repos.ClipsRepo)
	}

	// Delivery: HMAC Signer for signed asset delivery URLs.
	var deliveryPort search.AssetDeliveryService
	if cfg != nil && cfg.Security.DeliveryHMACSecret != "" {
		baseURL := cfg.External.VeloxBaseURL
		if baseURL == "" {
			baseURL = fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
		}
		signer, err := delivery.NewSigner(
			[]byte(cfg.Security.DeliveryHMACSecret),
			0, // use default 5m TTL
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

	_, _, searchAgg := registerSearchBackend(log, providerReg, root.Repos.ClipsRepo, wiring,
		embeddingReg,         // embeddings: EmbeddingChannelRegistry from Ollama pipeline
		vectorStoreForSearch, // vectorStore: Qdrant SearchAdapter from Process bundle
		mediaRepo,            // mediaRepo: ClipsRepository → MediaReadRepository
		deliveryPort,         // delivery: HMAC Signer for signed URLs
		rerankerPort,         // reranker: optional CrossEncoder post-retrieval stage
	)
	_ = searchAgg // for symmetry with pre-PR4 inline pattern; the var stays nil when providerReg is nil
	wiring.idempotencyHandler = idemHandler

	// Step 2 — Wikipedia: ScriptFlow is invoked by registerScripts
	// (registry_script.go) AFTER registerInternalModules returns.
	// Moving wireScriptFlow inside registerInternalModules here
	// would require passing the registry back up the call chain,
	// which the orchestrator already does.
	// The canonical ScriptFlow call lives in Step 3 of the orchestrator.

	// Step 3 — Artlist (PR4d-chunk2): takes *ArtlistBundle + vectorStore.
	if err := registerArtlist(ctx, registry, log, cfg, root, wiring); err != nil {
		return err
	}

	// Step 4 — YouTubeClip (PR4d-chunk2): 4 direct narrow args + ProviderRegistry.
	// ProviderRegistry is not yet populated when WireYouTubeClip runs —
	// the handler's constructor resolves providers lazily so it's fine
	// to pass the empty registry here; it will be populated by the time
	// HTTP requests arrive.
	if err := registerYouTubeClip(registry, log, cfg, root, wiring, searchAgg); err != nil {
		return err
	}

	// Step 5 — MediaIngest (bundle-driven; reuses root.<bundle> paths).
	// PR8: idemHandler installed on POST /api/media/ingest.
	//
	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): the
	// bundle roots the canonical outbox.Dispatcher into WireMediaIngest
	// so the 4 NewClipStoreAdapter + NewClipsRegistry ctor calls in
	// module_media.go get a non-nil mutations SSOT (production
	// canonical path).
	//
	// F2.7 (June 2026): Publisher (root.Drive.Publisher) wired in.
	// The 4 NewLifecycleFromDeps calls inside WireMediaIngest
	// thread Publisher + DriveReader to the lifecycle service —
	// Drive writes go through DestinationRegistry/RequireSubpath/
	// ConflictPolicy, Drive reads (reconcile) via DriveReader.
	mediaIngestW, mediaIngestErr := WireMediaIngest(cfg, log, &MediaIngestBundle{
		DB:                root.DB,
		Assets:            root.Repos.Assets,
		DriveUploader:     root.Drive.driveUploader,
		Lifecycle:         root.Drive.Lifecycle,
		Publisher:         root.Drive.Publisher,
		ImageRepo:         root.Repos.ImageRepo,
		VoiceoverRepo:     root.Repos.VoiceoverRepo,
		ClipsRepo:         root.Repos.ClipsRepo,
		AssetIndexService: root.Search.AssetIndexService,
		PrebuiltService:   root.Domains.IngestService,
		Dispatcher:        root.Outbox.Dispatcher,
	}, idemHandler)
	wiring.MediaIngest = mediaIngestW
	if mediaIngestErr != nil {
		log.Warn("failed to wire module", zap.String("module", "MediaIngest"), zap.Error(mediaIngestErr))
	} else if mediaIngestW != nil && mediaIngestW.Module != nil {
		if err := tryRegisterModuleStrict(registry, log, mediaIngestW.Module, WithRegistrationPoint("register.MediaIngest")); err != nil {
			return fmt.Errorf("wire registry: media-ingest: %w", err)
		}
	}

	// Step 6 — Scraper (infra-only).
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
		return fmt.Errorf("wire registry: scraper: %w", err)
	}

	// Step 7 — FullImages (bundle-driven; ImageService + ImagesDir).
	//
	// PR-IMAGES-FULLIMAGES-IMAGE-ONLY (2026-07-10, CUTOVER phase): the
	// pre-CUTOVER 4-field bundle (ImageService + FfmpegProc + Publisher
	// + ImagesDir) is REDUCED to 2 fields (ImageService + ImagesDir)
	// — the retired FfmpegProc + Publisher deps were ONLY consumed
	// by the now-retired Ken Burns video pipeline inside
	// mediafullimages.NewService.
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
		wiring.FullImages = nil
	} else if fullW != nil && fullW.Module != nil {
		wiring.FullImages = fullW
		if err := tryRegisterModuleStrict(registry, log, fullW.Module, WithRegistrationPoint("register.FullImages")); err != nil {
			return fmt.Errorf("wire registry: full-images: %w", err)
		}
		log.Info("registerInternalModules Step 7 FullImages pipeline mounted")
	} else {
		wiring.FullImages = nil
	}

	// Step 8 — StockPipeline (bundle-driven). Delegates to WireStockPipeline
	// which constructs every dep (DB, Cutter, Renderer, yt-dlp, Fetch,
	// SourceStager, Finalizer) and routes through BuildStockBundle.
	wiring.StockPipeline = nil
	stockW, stockErr := WireStockPipeline(cfg, log, root)
	if stockErr != nil {
		log.Warn("registerInternalModules Step 8 WireStockPipeline failed (godlike/07 fail-closed: typed sentinel surfaced, /api/stock-pipeline/* may return 404 or 503 depending on which gate fired)", zap.Error(stockErr))
	} else if stockW != nil && stockW.Module != nil {
		wiring.StockPipeline = stockW
		if err := tryRegisterModuleStrict(registry, log, stockW.Module, WithRegistrationPoint("register.StockPipeline")); err != nil {
			return fmt.Errorf("wire registry: stock-pipeline: %w", err)
		}
		if stockW.Service != nil && root.Jobs != nil && root.Jobs.Service != nil {
			if err := stockW.Service.RegisterHandler(root.Jobs.Service); err != nil {
				return fmt.Errorf("wire registry: stock-pipeline: register handler: %w", err)
			}
		}
		log.Info("registerInternalModules Step 8 stock pipeline mounted")
	}

	_ = ctx // unused at this level; consumers (Artlist, ScriptFlow) use it
	return nil
}

// registerArtlist wires the Artlist module via the canonical
// `artlist.Build(deps) (api.Descriptor, error)` contract (Blocco
// C1-Step 3, June 2026 — `internal/api/assets/artlist/module.go`).
//
// FASE-6 image-territories cutover (June 2026) retired WireArtlist and
// stubbed this body to log-only; routes /api/artlist/* return 404 in
// production since. Initial commit attempt of a full reversal exposed
// 3 first-order blockers (code-reviewer audit, July 2026):
//
//	(a) artlist.NewService validates ONLY `deps.Publisher`; the 8
//	    forward-pointer fields (MediaProcessor / LifecycleService /
//	    Searcher trio + Stager / MetadataWriter / AssetProcRepo+Ver+Loc)
//	    flow into the service struct without nil-check; nil-deref panic
//	    on /run / /recommend path is a godlike/07 fake-availability
//	    violation unless gates are tightened or forward-pointers are
//	    wired.
//	(b) ClipsRepository satisfies `artlist.AssetStore` 1:1 ONLY if
//	    the 7 method signatures (Get / Upsert / SearchByTerms /
//	    SearchClips / CountClips / LastUpdatedAtForTerm /
//	    UpdateSearchTerms) match exactly — the operator-narrative
//	    "PR2.5 mirrored" was not verified at compile time against the
//	    concrete receiver (would need a `clipsRepoAssetStoreAdapter`
//	    shim if signatures drift).
//	(c) `artlist.Build(Dependencies{...})` requires `Jobs` field type
//	    `jobservice.Service` (per `internal/domain/job/job.go::Service`)
//	    — a typed return of `jobdomain.Service` to that field compiles
//	    only if `jobdomain.Service` is the same interface (low risk
//	    given the import alias `jobservice` is canonical, but
//	    unverified in this commit window).
//
// Per godlike/07 no-fake-availability, the safe path is to STAY
// log-only here and let the 8 forward-pointers close via the
// PR-ARTLIST-* follow-ups. Each forward-pointer is filed as a
// `linked_issue` under `architecture/current.yaml#ART-001` (pending —
// the current.yaml file has a pre-existing parse error at
// line 1808 unrelated to this commit; the wave-tracker entry is
// deferred until file-unblock lands).
// registerArtlist wires the Artlist module via the canonical
// artlist.Build(deps) (api.Descriptor, error) contract.
//
// ART-001 reversal (July 2026): the FASE-6 stub is replaced with the
// full re-introduction of the canonical WireArtlist composition path.
// godlike/07 fail-closed: any of the 4 mandatory gates (Publisher /
// Dispatcher / ClipsRepo / Jobs.Service) nil → WireArtlist returns a
// typed error which we downgrade to log.Warn + skip-route + return-nil
// (composition boot MUST NOT abort because Artlist is optional in the
// architecture). Read-only endpoints (/stats, /diagnostics, /search/live)
// remain live even with forward-pointers nil; write endpoints return 503
// via the handler's nil-tolerance discipline (godlike/07 honest disclosure).
//
// Forward-pointer nil fields (8 in ServiceDeps + 1 in Build(Dependencies))
// are tagged inline in build_bundles_artlist.go with linked_issue ids
// pointing at architecture/current.yaml#ART-001.linked_issues. Each
// follows the godlike/07 EXPAND-phase discipline: nil is intentional
// + documented, not a placeholder to be patched in a future commit.
func registerArtlist(ctx context.Context, registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, wiring *RegistryWiring) error {
	_ = ctx

	// Feature-flag gate: composition-stable no-op when the operator has
	// not enabled the artlist capability. Pre-dates FASE-6 cutover.
	if !cfg.Features.ArtlistEnabled {
		log.Info("registerArtlist: feature disabled (cfg.Features.ArtlistEnabled=false); skipping route registration")
		wiring.ArtlistSvc = nil
		return nil
	}

	// Build the canonical ArtlistBundle from ComposeRoot. DriveClient is
	// intentionally nil (FASE-6 retired the raw *gdrive.Service reach-through
	// on this capability; the Pattern 0 publisher/reader/lifecycle trio below
	// replaces it without exposing the SDK).
	artlistWiring, err := WireArtlist(
		ctx,
		log,
		cfg,
		&ArtlistBundle{
			DB:                 root.DB,
			Assets:             root.Repos.Assets,
			ClipsRepo:          root.Repos.ClipsRepo,
			DriveClient:        nil,
			DriveUploader:      root.Drive.driveUploader,
			Publisher:          root.Drive.Publisher,
			AssetIndexService:  root.Search.AssetIndexService,
			ClipIndexerService: root.Process.ClipIndexerService,
			MediaProcessor:     root.Process.MediaProcessor,
			Jobs:               root.Jobs,
			CatalogSyncService: root.Sync.CatalogSync,
		},
		root.Outbox.Dispatcher,
		root.Drive.Reader,
		root.Drive.Lifecycle,
		root.Domains.MetaWriter,
		root.Drive.DestResolver,
	)
	if err != nil {
		// godlike/07 fail-closed: when ArtlistEnabled=true (gated above at
		// the feature-flag check), a missing mandatory dep ABORTS BOOT with
		// a typed error. The previous log.Warn + skip-route + return-nil
		// was a godlike/07 fake-availability violation — operators saw a
		// green boot with no indicator of WHICH dep was missing, and
		// `curl /api/artlist/*` returned 404 without surfacing the gate
		// that fired.
		// Phase 1 (Fase 1, July 2026) surfaces the ErrArtlistDepMissing
		// sentinel via errors.As(&missing) so the operator log carries
		// the canonical Kind + Field tuple. The wrapping
		// `registerArtlist aborting boot (godlike/07 fail-closed)` keeps
		// the diagnostic literal searchable in production logs (mirrors
		// the pre-PR wire-typed-error literal at the WireArtlistJobBindings
		// failure branch).
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

	wiring.ArtlistSvc = artlistWiring

	// PR-P2-FAILCLOSED-JOB (July 2026): composition-time fail-closed
	// contract per user spec — "fallisci l'avvio con un typed error
	// (no warning silenzioso)". The previous pre-PR behavior was
	// log.Warn + continue, which violated godlike/07: media.artlist
	// jobs would queue to dead-letter forever without a consumer.
	// The wrapper chain surfaces ErrArtlistConsumerRegistrationFailed
	// at the top so test fixtures (TestRegisterArtlist_AbortsOnJobBindFailure)
	// can branch on the typed sentinel. WireArtlistJobBindings already
	// wraps the inner error with that sentinel internally.
	if err := WireArtlistJobBindings(artlistWiring.Service, root.Jobs); err != nil {
		_ = artlistWiring.Service.Close()
		return fmt.Errorf("wire registry: artlist: %w", err)
	}

	log.Info("registerArtlist: ART-001 reversal milestone complete",
		zap.String("descriptor_module_name", artlistWiring.Module.Name()),
	)
	return nil
}

// registerYouTubeClip wires the YouTubeClip module via the canonical
// youtube.Build(deps) (api.Descriptor, error) contract, matching the
// artlist / clips / voiceover / soundeffect / register precedent.
//
// The Handler is constructed inside Build and captured by the
// returned YouTubeDescriptor's Module closure. The composition
// site type-asserts ONCE to *youtubeapi.YouTubeDescriptor
// (fail-closed) and reuses the concrete for the
// wiring.YouTubeClip wiring handle.
func registerYouTubeClip(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, wiring *RegistryWiring, searchAgg *providers.SearchAggregator) error {
	if !cfg.Features.YouTubeEnabled {
		log.Info("registerYouTubeClip: YouTube feature is disabled; skipping HTTP route registration")
		wiring.YouTubeClip = nil
		return nil
	}

	descriptor, err := youtubeapi.Build(youtubeapi.Dependencies{
		Service:          root.Domains.YoutubeClipService,
		Jobs:             root.Jobs.Facade,
		ToolChecker:      toolCheckerAdapter,
		Idempotency:      wiring.idempotencyHandler,
		SearchAggregator: searchAgg,
		EnabledFunc:      func() bool { return cfg.Features.YouTubeEnabled },
		Logger:           log,
	})
	if err != nil {
		return fmt.Errorf("registerYouTubeClip: youtube.Build: %w", err)
	}
	yd, ok := descriptor.(*youtubeapi.YouTubeDescriptor)
	if !ok || yd == nil {
		return fmt.Errorf("registerYouTubeClip: youtube.Build returned unexpected descriptor type %T (want *youtubeapi.YouTubeDescriptor)", descriptor)
	}
	wiring.YouTubeClip = &YouTubeClipWiring{
		Module:  yd.Module,
		Service: yd.Service,
	}
	log.Info("created YouTubeClip module via youtube.Build (Blocco C1-Step 4)")
	return tryRegisterModuleStrict(registry, log, yd, WithRegistrationPoint("register.YouTubeClip"))
}

// registerJobsRoute registers the /jobs route module. Inlined from the
// pre-PR4 "1) Jobs" numbered block.
//
// Blocco C1-Step 13 (June 2026): Jobs capability is now built via
// the canonical jobs.Build(deps) (api.Descriptor, error) contract,
// matching the artlist / youtube / clips / stock / voiceover /
// soundeffect / register / diagnostics / search precedent. The
// Handler is constructed inside Build and captured by the
// returned JobsDescriptor's Module closure. The composition site
// type-asserts ONCE to *jobs.JobsDescriptor (fail-closed) and
// reuses the concrete for the tryRegisterModuleStrict call (the
// concrete *JobsDescriptor satisfies api.Descriptor structurally).
// The jobs capability has no non-HTTP consumer in the codebase
// (the 7 routes /jobs/* are the entire public surface, reachable
// only via HTTP), so the Descriptor surface is the smallest
// possible — just `Module` field + forwarder methods (matches the
// stock / voiceover / soundeffect / register / diagnostics /
// search precedent exactly).
func registerJobsRoute(registry *module.Registry, log *zap.Logger, root *ComposeRoot) error {
	jobsDescriptor, err := jobsapi.Build(jobsapi.Dependencies{
		Service:     root.Jobs.Service,
		Stats:       root.Jobs.Service,           // *appjobs.Service satisfies both domainjob.Service + appjobs.JobStatsReader
		EnabledFunc: func() bool { return true }, // jobs is always on in production
		ModuleOpts:  nil,                         // no per-feature middleware (matches pre-Step-13 wiring)
		Logger:      log,
	})
	if err != nil {
		return fmt.Errorf("wire registry: jobs: %w", err)
	}
	jd, ok := jobsDescriptor.(*jobsapi.JobsDescriptor)
	if !ok || jd == nil {
		return fmt.Errorf("wire registry: jobs: jobs.Build returned unexpected descriptor type %T (want *jobsapi.JobsDescriptor)", jobsDescriptor)
	}
	log.Info("created Jobs module")
	return tryRegisterModuleStrict(registry, log, jd, WithRegistrationPoint("register.Jobs"))
}
