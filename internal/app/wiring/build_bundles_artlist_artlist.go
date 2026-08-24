// Package app — build_bundles_artlist_artlist.go
//
// Artlist services-core composition root. The WireArtlist orchestrator
// here delegates provider-side construction to
// build_bundles_artlist_providers.go::constructArtlistProviders and
// publisher-side construction to
// build_bundles_artlist_publishers.go::constructArtlistRepositories.
//
// Composition order (godlike/06 SSOT):
//  1. Upfront fail-closed gates (5 wiring + 1 cfg + 1 Indexer + 1 Finalizer).
//  2. constructArtlistRepositories FIRST — the audit adapter must
//     exist before constructArtlistProviders can wire it into the
//     ResolverConfig.AuditRepository slot (this matches the inline
//     construction order in the pre-split WireArtlist body).
//  3. constructArtlistProviders SECOND — receives the audit adapter
//     from the repos bundle.
//
// godlike/06 SSOT: this file owns the canonical Pattern-0 adapter
// wiring of the artlist capability.
// godlike/07 fail-closed: 7 mandatory gates are checked UPFRONT.
// godlike/06: SemanticEnricher is the canonical app-layer wrapper
// matching artlist.MetadataWriter.Enrich (enrich signature verbatim).
package wiring

import (
	"context"
	"fmt"
	artlistapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	artlist "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	scripts_adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	ytadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/artlist/scraper"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	searchtextinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/searchtext"
	sqliteSearch "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	sqliteMediaMemory "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"go.uber.org/zap"
)

// WireArtlist constructs *artlist.Service + *ArtlistDescriptor from the canonical
// wiring.ArtlistBundle populated by registerArtlist + the 5 wiring.ComposeRoot receiver-fields
// (Dispatcher / Drive.Reader / Drive.Lifecycle / MetaWriter / DestResolver) that
// were not pre-exposed on wiring.ArtlistBundle by PR4d-chunk2 convention. Each of the
// 5 is a DIRECT receiver from wiring.ComposeRoot — not an adapter shim (godlike/06 SSOT).
//
// godlike/07: 7 mandatory gates checked UPFRONT. The first 4 are
// runtime-wiring gates (Publisher / Dispatcher / ClipsRepo / Jobs.Service);
// nil on any yields a typed error which the caller (registerArtlist)
// downgrades to log.Warn + skip-route + return-nil. Gate #5 is the
// config-validity check (Node scraper URL when artlist_enabled=true)
// and lives in validateArtlistScraperURL for direct unit-testability
// per godlike/06 SSOT.
// godlike/06: SemanticEnricher is the canonical app-layer wrapper matching
// artlist.MetadataWriter.Enrich (enrich signature verbatim).
func WireArtlist(
	ctx context.Context,
	log *zap.Logger,
	cfg *config.Config,
	bundle *wiring.ArtlistBundle,
	dispatcher *outbox.Dispatcher,
	reader drivepkg.Reader,
	lifecycle drivepkg.FileLifecycle,
	metaWriter semantic.MetadataWriterPort,
	destResolver asset.Resolver,
	textTrackFanOut ...*texttracks.MaterializeFanOut,
) (*wiring.ArtlistWiring, error) {
	_ = ctx

	// godlike/07 fail-closed: 5 mandatory UPFRONT gates (4 wiring + 1 cfg).
	// Phase 1 (Fase 1, July 2026): each gate now returns the typed
	// ErrArtlistDepMissing{Kind, Field} sentinel so orchestrators can
	// branch on intent via errors.As(&missing) and report WHICH dep
	// failed. The previous fmt.Errorf chain lacked structured Kind/Field
	// and forced log-line grep — Phase 1 makes the diagnostic
	// programmatically reachable.
	if bundle == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindRunRepo, Field: "bundle"}
	}
	if bundle.Publisher == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindPublisher, Field: "bundle.Publisher"}
	}
	if dispatcher == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindDispatcher, Field: "dispatcher"}
	}
	if bundle.ClipsRepo == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindClipsRepo, Field: "bundle.ClipsRepo"}
	}
	if bundle.Jobs == nil || bundle.Jobs.Service == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindJobsService, Field: "bundle.Jobs.Service"}
	}
	// gate #6 — Indexer (Qdrant clipindexer port).
	// Per user spec literal "Qdrant indexer" listing + per godlike/07
	// fail-closed: nil on the indexer would cause runtime nil-deref at
	// the first outbox-stage /run invocation that reaches indexing.
	// Composition-time rejection via typed sentinel is the canonical
	// contract; the diagnostic endpoint (Fase 2 follow-up) reads the
	// Kind to surface WHICH dep the operator forgot to wire.
	if bundle.ClipIndexerService == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindIndexer, Field: "bundle.ClipIndexerService"}
	}
	if bundle.Committer == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindFinalizer, Field: "bundle.Committer"}
	}

	// job.Service alias pin is verified at compile time (Pattern 0 + AGENTS.md).

	// gate #7 — Finalizer (canonical transactional AssetTxFinalizer).
	// Per user spec literal "finalizer" listing + per godlike/07
	// fail-closed: the constructor's nil-discard path is contractually
	// permitted (a future config-validation branch could return nil
	// on incompatible schema). Composition-time rejection via typed
	// sentinel pins the invariant; `Field: "finalizerTx"` so the
	// diagnostic surfaces the source-path beside the well-known Kind.
	finalizerTx := assetfinalizer.NewAssetTxFinalizer(log, bundle.Committer)
	if len(textTrackFanOut) > 0 && textTrackFanOut[0] != nil {
		finalizerTx.WithFanOut(textTrackFanOut[0])
	}
	if finalizerTx == nil {
		return nil, ErrArtlistDepMissing{Kind: DepKindFinalizer, Field: "finalizerTx"}
	}

	// godlike/07 fail-closed: gate #5 (ART-002 P0.1, July 2026) — config
	// validity check. When artlist_enabled=true the Node scraper server
	// URL MUST be configured (env VELOX_ARTLIST_SCRAPER_SERVER_URL,
	// PR-ARTLIST-CONFIG-PREFIX cutover from the bare
	// ARTLIST_SCRAPER_SERVER_URL); without it the scraper constructor
	// silently degrades to per-call exec fallback (heavier + less
	// reliable). This gate pins the no-fake-availability contract at
	// the composition-root layer — see validateArtlistScraperURL
	// godoc for the underlying rationale and the two valid escape hatches
	// (disable Artlist via VELOX_FEATURE_ARTLIST_ENABLED=false, or set
	// VELOX_ARTLIST_SCRAPER_SERVER_URL to a real Node-scraper URL).
	if err := validateArtlistScraperURL(cfg); err != nil {
		return nil, err
	}
	_ = (job.Service)(bundle.Jobs.Service)

	log.Info("WireArtlist: ART-001 reversal wiring starting",
		zap.String("root_path", "/api/artlist/*"),
		zap.Bool("godlike_07_fail_closed", true),
	)

	// Publisher-side bundle (helper in publishers.go). Must run BEFORE
	// providers because the audit adapter is required by the Resolver
	// config (see constructArtlistProviders parameter auditAdapter).
	repos, err := constructArtlistRepositories(bundle, log)
	if err != nil {
		return nil, err
	}

	// Provider-side bundle (helper in providers.go). Receives the audit
	// adapter from the repos bundle to wire into ResolverConfig.AuditRepository.
	providers := constructArtlistProviders(cfg, log, bundle, repos.DownloadAuditAdapter, bundle.MediaExec)

	// The Node scraper provider is both a Searcher and a DetailFetcher.
	// Reuse the same instance so /search and /import share the browser pool.
	scraperProvider := scraper.New(scraper.Config{
		ServerURL:  cfg.External.ArtlistScraperServerURL,
		ScraperDir: cfg.External.NodeScraperDir,
		ScriptName: "artlist_search.js",
	}, log)

	// godlike/06 SSOT: SemanticEnricher is the canonical app-layer wrapper for
	// semantic.MetadataWriterPort; its Enrich(ctx, clip, term) signature matches
	// artlist.MetadataWriter.Enrich exactly (semantic_enricher.go:147). The 8
	// constructor args are all DIRECT receivers — no shim layer.
	searchTextRegistry := searchtextinfra.NewRegistry()
	semanticEnricher := artlist.NewSemanticEnricher(artlist.SemanticEnricherDeps{
		Repo:             bundle.ClipsRepo,
		Indexer:          bundle.ClipIndexerService,
		MetaWriter:       metaWriter,
		SearchDocBuilder: searchtextinfra.NewAssetSearchDocumentBuilder(searchTextRegistry),
		Publisher:        bundle.Publisher,
		Reader:           reader,
		Dispatcher:       dispatcher,
		Lifecycle:        lifecycle,
		Log:              log,
	})

	// PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY-CUTOVER (July 2026): inject
	// the Resolver into the media processor's ArtlistDownloader port
	// so downloadStep routes Artlist clips through the canonical
	// Resolver instead of the legacy downloadViaScraper method.
	// The adapter is the SINGLE translation site between the
	// processor's narrow ArtlistDownloader interface and the
	// Resolver's Download(artlist.DownloadRequest) method.
	wireArtlistProcessorDownloader(log, bundle, providers.ArtlistDownloader)

	// PR-ARTLIST-MANDATORY-TRANSCRIPTION (July 2026): the transcriber is
	// the same adapter used by the YouTube registrar. Reusing it here
	// keeps the Whisper wiring in a single place and satisfies the
	// artlist.Transcriber port via implicit interface satisfaction.
	transcriber := ytadapters.NewSourcingTranscriberAdapter(cfg, log)

	// 19-field ServiceDeps literal via nested named-struct init (8 ServicePorts
	// + 11 ServiceDependencies). 3 forward-pointer nil fields tagged with
	// linked_issue id per architecture/current.yaml#ART-001.linked_issues
	// (PR-ARTLIST-SEARCHERS closed 2026-07-04: 3 searchers wired inline).
	localSearcher := sqliteSearch.NewArtlistSQLiteSearcher(bundle.ClipsRepo)

	service, err := artlist.NewService(artlist.ServiceDeps{
		ServicePorts: artlist.ServicePorts{
			// ServicePorts (9) — 9 DIRECT (PR-ARTLIST-SEARCHERS closed: 3 searchers
			// constructed inline from cfg + the canonical infra concretes; runtime
			// returns ErrUnavailable when API keys are empty per godlike/07 graceful
			// degradation, instead of nil-tolerated 503 at the handler layer).
			AssetStore:      bundle.ClipsRepo,
			LocalSearcher:   localSearcher,
			Indexer:         bundle.ClipIndexerService,
			MetadataWriter:  semanticEnricher,
			Publisher:       bundle.Publisher,
			ScraperSearcher: scraperProvider,
			PixabaySearcher: providers.PixabaySearcher,
			PexelsSearcher:  providers.PexelsSearcher,
			DetailFetcher:   scraperProvider,
			Stager:          providers.ArtlistStager,
			IsLiveProbe:     providers.IsLiveProbe,
			// PR-ARTLIST-PERSIST-FIX (2026-07-04): mandatory
			// RunRepository port wiring (godlike/07 fail-closed).
			// The concrete in sqlite/assets/ is wrapped via the
			// canonical artlistRunsRepoAdapter (composition-root,
			// owns the import-cycle pin) so the ServicePorts field
			// sees the canonical port type, not the infra-side
			// local Record interface.
			//
			// ART-002 P0 fix (July 2026): the line that follows
			// MUST stay outside the comment block; a previous
			// refactor collapsed it onto the same source line as
			// `local Record interface. ...` which Go's parser
			// silently consumed as comment text — that swallowed
			// the wiring and forced artlist.NewService to fail with
			// ErrRunRepositoryUnavailable, leaving /api/artlist/*
			// unmounted on main.
			RunRepository:  repos.RunsAdapter,
			SearchStrategy: artlist.ArtlistSearchStrategy(cfg.External.ArtlistSearchStrategy),
			// Phase 2 / Fase 2 (July 2026): SystemProber port is the
			// 10-probe fan-out node. Injected by composition root
			// (canonical owner of the wire shape per godlike/06 SSOT);
			// the concrete AdminSystemProber services probes
			// sequentially with per-probe timeout. Commit 1 wires only
			// the upstream probes (Commit 2/3/4 replace 6 stubs with
			// real probe logic).
			SystemProber: providers.SystemProber,
			// MediaMemory linking: create media_concepts / media_bindings
			// after a clip is materialized (Maya demo graph).
			MediaMemoryConceptRepo: sqliteMediaMemory.NewConceptsRepository(bundle.DB.DB),
			MediaMemoryBindingRepo: sqliteMediaMemory.NewBindingsRepository(bundle.DB.DB),
			MediaMemoryNormalizer:  mediamemory.NewDefaultNormalizer(""),
			Transcriber:            transcriber,
		},
		ServiceDependencies: artlist.ServiceDependencies{
			// ServiceDependencies (10) — grouped into sub-bundles to
			// respect the AGENTS.md 8-field cap.
			Infra: artlist.ArtlistInfraDeps{
				Cfg:    cfg,
				Log:    log,
				MainDB: bundle.DB.DB,
			},
			Ports: artlist.ArtlistPortDeps{
				Dispatcher: dispatcher,
			},
			Domain: artlist.ArtlistDomainDeps{
				MediaProcessor:    bundle.MediaProcessor,
				AssetDestResolver: destResolver,
				JobsSvc:           bundle.Jobs.Service,
			},
			Repos: artlist.ArtlistRepoDeps{
				AssetProcRepo:       repos.AssetProcRepo,
				AssetVerRepo:        repos.AssetVerRepo,
				LocationRepository:  nil, // retired from artlist service wiring
				RenditionRepository: repos.RenditionRepo,
				TextTrackRepo:       bundle.TextTrackRepo,
			},
			Finalizer: artlist.ArtlistFinalizerDeps{
				// PR-ARTLIST-FINALIZER (July 2026): canonical transactional
				// asset finalizer. Replaces the legacy dispatchBridge path.
				// Phase 1 (Fase 1, July 2026): finalizerTx is declared above
				// (gate #7) so the typed sentinel path is the SOLE
				// canonical owner of the fail-closed check; the previous
				// inline-construction shape masked the nil-discard path.
				AssetFinalizerTx: finalizerTx,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: artlist.NewService: %w", err)
	}

	// PR-ARTLIST-RECOMMEND-ADAPTER (closed 2026-07-04): wire the
	// canonical *scripts.ClipResolver (Resolve method, ID-based
	// dispatch) into the handler-side artlist.ClipResolverPort
	// (Recommend method) via the new clipResolverRecommendAdapter
	// (field-weighted Jaccard scoring layer). The adapter is the
	// SINGLE translation site per godlike/06 SSOT. godlike/07
	// fail-closed: nil ClipsRepo (mandatory gate above) or nil
	// canonical would yield a nil adapter; the handler's nil-
	// tolerance continues to return 503 on /recommend in that
	// case (unchanged runtime contract for unavailable canonical).
	bundle.ClipResolver = wiring.NewClipResolverRecommendAdapter(
		scripts_adapters.NewClipResolver(bundle.ClipsRepo, log),
		log,
	)

	descriptor, err := artlistapi.Build(artlistapi.Dependencies{
		Service:     service,
		CatalogSync: bundle.CatalogSyncService,
		// PR-ARTLIST-ENQUEUE-SERVICE (July 2026): the /run enqueue path
		// moved into artlist.Service.EnqueueRun (wired above via
		// ServiceDependencies.Domain.JobsSvc), so the module no longer
		// needs a Jobs dependency — the handler only parses/validates/
		// responds (godlike/06 SSOT).
		ClipResolver: bundle.ClipResolver,
		CfgPort:      newArtlistConfigAdapter(cfg),
		EnabledFunc:  func() bool { return cfg.Features.ArtlistEnabled },
		ModuleOpts:   nil, // forward-pointer: PR-COMPOSITION-MODULE-OPTS
		Logger:       log,
	})
	if err != nil {
		_ = service.Close()
		return nil, fmt.Errorf("WireArtlist: artlist.Build: %w", err)
	}

	// Type-assert the canonical concrete (Blocco C1-Step 3: descriptor is
	// api.Descriptor at the wire layer; the *artlistapi.ArtlistDescriptor
	// concrete carries Module + Service fields). Mirrors registerYouTubeClip
	// precedent (335-340 of registry_internal_modules.go).
	ad, ok := descriptor.(*artlistapi.ArtlistDescriptor)
	if !ok || ad == nil {
		_ = service.Close()
		return nil, fmt.Errorf("WireArtlist: artlist.Build returned unexpected descriptor type %T (want *artlistapi.ArtlistDescriptor)", descriptor)
	}

	// ProviderAssets is built by the single composition-root catalog builder.
	providerAssetsRegistry, err := buildProviderAssetCatalog(service, providers.PexelsSearcher, providers.PixabaySearcher)
	if err != nil {
		_ = service.Close()
		return nil, fmt.Errorf("WireArtlist: provider asset catalog: %w", err)
	}

	log.Info("WireArtlist: ART-001 reversal wiring complete",
		zap.String("descriptor_name", ad.Name()),
		zap.Strings("provider_assets", providerAssetsRegistry.Names()),
		zap.Bool("godlike_06_ssot", true),
	)
	return &wiring.ArtlistWiring{
		Module:            ad.Module,
		Service:           ad.Service,
		ProviderAssets:    providerAssetsRegistry,
		ArtlistDownloader: providers.ArtlistDownloader,
		LicenseRepo:       repos.LicenseRepo,
		ReleaseRepo:       repos.ReleaseRepo,
		RenditionRepo:     repos.RenditionRepo,
	}, nil
}

// validateArtlistScraperURL is the canonical fail-closed gate #5
// (ART-002 P0.1, July 2026) for WireArtlist. Returns a non-nil error if
// the Artlist feature is enabled but the Node scraper server URL is
// empty.
//
// godlike/07 no-fake-availability: deployments with feature enabled but
// missing URL MUST NOT succeed silently — the underlying scraper.New()
// field (ServerURL="") would otherwise pass through to per-call exec
// fallback (heavier + less reliable) and break /run invocations on first
// use rather than at startup. This gate pins the fail-closed contract at
// the composition-root layer; the underlying scraper.New() still accepts
// URL="" for backward compat with non-Artlist providers (test fixtures,
// smoke tests, the etc/script artifact paths).
//
// godlike/06 SSOT: the gate is the SINGLE canonical owner of this check;
// the 4 TDD tests in build_bundles_artlist_test.go target it directly via
// (cfg) → error return. Promotion to WireArtlist is via a single call
// site (no inline duplicate logic anywhere). If the underlying check
// moves to the scraper package later, mirror it here as a defense-in-depth
// gate (don't remove the composition-root check — the "fail fast at boot
// vs fail slow at first /run" distinction is operationally important).
//
// Escape hatches (documented in the returned error message):
//
//	(a) Disable Artlist:    VELOX_FEATURE_ARTLIST_ENABLED=false (default)
//	(b) Configure scraper:  VELOX_ARTLIST_SCRAPER_SERVER_URL=http://artlist-scraper:9123
//	                        (docker-compose.yml production setup, PR-ARTLIST-CONFIG-PREFIX
//	                        July 2026 renamed from the bare ARTLIST_SCRAPER_SERVER_URL)
func validateArtlistScraperURL(cfg *config.Config) error {
	if cfg == nil {
		return ErrArtlistDepMissing{
			Kind:   DepKindScraperURL,
			Field:  "cfg",
			Detail: "ART-002 P0.1 gate #5: cfg is nil; cannot evaluate scraper-URL fail-closed",
		}
	}
	if cfg.Features.ArtlistEnabled && cfg.External.ArtlistScraperServerURL == "" {
		return ErrArtlistDepMissing{
			Kind:   DepKindScraperURL,
			Field:  "cfg.External.ArtlistScraperServerURL",
			Detail: "ART-002 P0.1: cfg.Features.ArtlistEnabled=true but cfg.External.ArtlistScraperServerURL is empty — required env VELOX_ARTLIST_SCRAPER_SERVER_URL (without it the searcher chain silently degrades to per-call exec fallback). To disable Artlist set VELOX_FEATURE_ARTLIST_ENABLED=false",
		}
	}
	return nil
}

// WireArtlistJobBindings registers the Artlist job handler with the jobs dispatcher.
// Extracted from WireArtlist so the late-binding has a dedicated composition surface
// (mirrors wireYoutubeCatalogJobBindings precedent in build_bundles_youtube.go).
//
// godlike/07 no-fake-availability (PR-P2-FAILCLOSED-JOB, July 2026):
// any non-nil error from artlistSvc.RegisterHandler is wrapped with
// ErrArtlistConsumerRegistrationFailed so the upstream composition
// caller `registerArtlist` (registry_internal_modules.go) aborts boot
// with the typed sentinel — the previous silent-Warn + continue path
// was a fake-availability violation (media.artlist jobs would queue
// to dead-letter forever without a consumer). The composition-time
// fail-closed contract is the user-spec literal:
// "fallisci l'avvio con un typed error (no warning silenzioso)".
func WireArtlistJobBindings(artlistSvc *artlist.Service, jobsBundle *wiring.JobsBundle) error {
	if artlistSvc == nil {
		return fmt.Errorf("WireArtlistJobBindings: artlistSvc is nil")
	}
	if jobsBundle == nil || jobsBundle.Service == nil {
		return fmt.Errorf("WireArtlistJobBindings: jobsBundle.Service is nil")
	}
	if err := artlistSvc.RegisterHandler(jobsBundle.Service); err != nil {
		return fmt.Errorf("%w: %w", ErrArtlistConsumerRegistrationFailed, err)
	}
	// PR-P2-FAILCLOSED-JOB post-bind godlike/07 verification:
	// confirm the handler was actually bound. If the dispatcher
	// silently dropped the Register call (nil dispatcher or
	// handler-disabled flag), HasHandler would still return
	// false post-call — surface as the same typed sentinel so
	// the composition caller aborts rather than continuing with
	// an unwired consumer.
	if !jobsBundle.Service.HasHandler(media.TypeArtlistRun) {
		return fmt.Errorf("%w: post-bind HasHandler(media.artlist) returned false (dispatcher silently dropped the Register call?)",
			ErrArtlistConsumerRegistrationFailed)
	}
	if !jobsBundle.Service.HasHandler(media.TypeArtlistCacheRefresh) {
		return fmt.Errorf("%w: post-bind HasHandler(media.artlist_cache_refresh) returned false",
			ErrArtlistConsumerRegistrationFailed)
	}
	return nil
}
