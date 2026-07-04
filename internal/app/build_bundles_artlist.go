// Package app — build_bundles_artlist.go: composition-root surface for the
// Artlist module (ART-001 FASE-6 reversal, July 2026).
//
// godlike/06 SSOT: this file owns the canonical Pattern-0 wiring of the
// artlist capability, including the only *artlist.SemanticEnricher
// instantiation in the process — composition root is the canonical owner
// for adapter wiring by construction.
//
// godlike/07 no-fake-availability: 5 mandatory gates are checked UPFRONT
// (Publisher / Dispatcher / ClipsRepo / Jobs.Service are the 4 wiring
// gates; gate #5 = config validity for the Node scraper URL when
// artlist_enabled=true). nil on any of the first 4 yields a typed error,
// which registerArtlist downgrades to log.Warn + skip-route +
// return-nil. Gate #5 (scraper-server URL) is extracted to
// validateArtlistScraperURL for direct unit-testability per godlike/06
// SSOT — it aborts the wiring loudly with an actionable fix hint instead
// of silently degrading to per-call exec fallback at first /run. Operators
// see 404 on /api/artlist/* rather than a full-system boot abort.
//
// declared explicitly with a linked_issue cross-ref; see
// architecture/current.yaml#ART-001.linked_issues (godlike/07 EXPAND-phase
// discipline). The 3 repo fields (AssetProcRepo / AssetVerRepo / AssetLocRepo)
// are now WIRED via sqassets.NewAssetStoreSQLite (PRIORITÀ ASSOLUTA — nil would
// panic in run_orchestrator_stages.go); the 3 searcher fields
// (ScraperSearcher / PixabaySearcher / PexelsSearcher) are now WIRED inline from
// the canonical infra concretes (PR-ARTLIST-SEARCHERS closed 2026-07-04).
// Read-only endpoints (/stats, /diagnostics, /search/live) remain live; write
// endpoints (/run, /recommend, /sync-catalogs) no longer return 503 from the
// searcher-tier forward-pointers. The Build(Dependencies).ClipResolver field
// is now WIRED via the new clipResolverRecommendAdapter
// (PR-ARTLIST-RECOMMEND-ADAPTER, closed 2026-07-04) which bridges the
// handler-side artlist.ClipResolverPort.Recommend method to the canonical
// *scripts.ClipResolver.Resolve method + a real field-weighted Jaccard
// scoring layer (Name 0.30 + Filename 0.10 + Description 0.20 + Tags 0.30 +
// Transcript 0.10, see internal/app/clip_resolver_recommend_adapter.go). The
// /recommend endpoint now returns real recommendations (not 503) when the
// canonical resolver is available; nil canonical yields a nil adapter
// (godlike/07 fail-closed fast path) and the handler's nil-tolerance returns
// 503 only in that case. The audit-pin closure of the obsolete clipresolver
// forward-pointer is in architecture/deprecations.yaml#PR-ARTLIST-SYNCSERVICE
// (closed 2026-07-04) — the clipresolver package was already removed in a
// prior refactor, so the deprecation was paperwork only.
//
// Single-function shape (WireArtlist) mirrors the existing WireMediaIngest
// precedent in registry_internal_modules.go (Blocco C1-Step 3 scope).
//
// Riuso: ArtlistBundle struct (bundle_types.go, canonical per PR4d-chunk2)
// + newArtlistConfigAdapter (adapters_infra.go, already compile-time-pinned
// against artlist.ArtlistConfigPort).
package app

import (
	"context"
	"fmt"

	artlistapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/artlist"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	scripts_usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/fallback"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/scraper"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	clipindexer "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	mediaproc "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// Pattern 0 compile-time pins (AGENTS.md): canonical DIRECT receivers
// straight-satisfy the artlist ports. Drift in any signature surfaces as
// a build failure here rather than as a runtime panic on first dispatch.
var (
	_ artlistPkg.AssetStore = (*assets.ClipsRepository)(nil) // 7-method set via *AssetStoreSQLite method-promotion
	_ artlistPkg.Indexer    = (*clipindexer.Service)(nil)    // IndexClip + IsEnabled
	_ artlistPkg.Dispatcher = (*outbox.Dispatcher)(nil)      // EnqueueAndIndex + SaveDiscoveredAsset
	_ jobdomain.Service     = (*appjobs.Service)(nil)        // cross-package alias safety (Build Deps.Jobs + ServiceDeps.JobsSvc)
)

// WireArtlist constructs *artlist.Service + *ArtlistDescriptor from the canonical
// ArtlistBundle populated by registerArtlist + the 5 ComposeRoot receiver-fields
// (Dispatcher / Drive.Reader / Drive.Lifecycle / MetaWriter / DestResolver) that
// were not pre-exposed on ArtlistBundle by PR4d-chunk2 convention. Each of the
// 5 is a DIRECT receiver from ComposeRoot — not an adapter shim (godlike/06 SSOT).
//
// godlike/07: 5 mandatory gates checked UPFRONT. The first 4 are
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
	bundle *ArtlistBundle,
	dispatcher *outbox.Dispatcher,
	reader drivepkg.Reader,
	lifecycle drivepkg.FileLifecycle,
	metaWriter *semantic.MetadataWriter,
	destResolver asset.Resolver,
) (*ArtlistWiring, error) {
	_ = ctx

	// godlike/07 fail-closed: 5 mandatory gates UPFRONT (4 wiring + 1 config,
	// see validateArtlistScraperURL below). Gates #1-4 short-circuit in order.
	if bundle == nil {
		return nil, fmt.Errorf("WireArtlist: bundle is nil")
	}
	if bundle.Publisher == nil {
		return nil, fmt.Errorf("WireArtlist: bundle.Publisher is nil (F2.11 mandatory; artlist.NewService rejects with ErrPublisherUnavailable so we fail-closed upstream)")
	}
	if dispatcher == nil {
		return nil, fmt.Errorf("WireArtlist: dispatcher is nil (QDRANT-002 mandatory; artlist.NewSearchService rejects with ErrAssetMutationDispatcherUnavailable)")
	}
	if bundle.ClipsRepo == nil {
		return nil, fmt.Errorf("WireArtlist: bundle.ClipsRepo is nil (AssetStore port nil at first SearchByTerms call would panic)")
	}
	if bundle.Jobs == nil || bundle.Jobs.Service == nil {
		return nil, fmt.Errorf("WireArtlist: bundle.Jobs.Service is nil (Build dep + JobsSvc; /run path unreachable)")
	}

	// jobdomain.Service alias pin is verified at compile time (Pattern 0 + AGENTS.md).

	// godlike/07 fail-closed: gate #5 (ART-002 P0.1, July 2026) — config
	// validity check. When artlist_enabled=true the Node scraper server
	// URL MUST be configured (env ARTLIST_SCRAPER_SERVER_URL); without it
	// the scraper constructor silently degrades to per-call exec fallback
	// (heavier + less reliable). This gate pins the no-fake-availability
	// contract at the composition-root layer — see validateArtlistScraperURL
	// godoc for the underlying rationale and the two valid escape hatches
	// (disable Artlist via VELOX_FEATURE_ARTLIST_ENABLED=false, or set
	// ARTLIST_SCRAPER_SERVER_URL to a real Node-scraper URL).
	if err := validateArtlistScraperURL(cfg); err != nil {
		return nil, err
	}
	_ = (jobdomain.Service)(bundle.Jobs.Service)

	log.Info("WireArtlist: ART-001 reversal wiring starting",
		zap.String("root_path", "/api/artlist/*"),
		zap.Bool("godlike_07_fail_closed", true),
	)

	// godlike/06 SSOT: HTTPSelfLoopProbe is the canonical app-layer wrapper
	// for *Probe; its Probe(ctx) (bool, error) signature matches
	// artlist.IsLiveProbe exactly (http_live_probe.go). Composition root
	// owns the resolution of baseURL (cfg.External.VeloxBaseURL > localhost
	// fallback) + timeout (5s default per DefaultProbeTimeout in artlist pkg).
	probeBaseURL := cfg.External.VeloxBaseURL
	if probeBaseURL == "" {
		probeBaseURL = fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
	}
	isLiveProbe := artlistPkg.NewHTTPSelfLoopProbe(
		probeBaseURL,
		"/api/artlist/stats",
		artlistPkg.DefaultProbeTimeout,
		log,
	)

	// godlike/06 SSOT: SemanticEnricher is the canonical app-layer wrapper for
	// *semantic.MetadataWriter; its Enrich(ctx, clip, term) signature matches
	// artlist.MetadataWriter.Enrich exactly (semantic_enricher.go:147). The 8
	// constructor args are all DIRECT receivers — no shim layer.
	semanticEnricher := artlistPkg.NewSemanticEnricher(
		bundle.ClipsRepo,
		bundle.ClipIndexerService,
		metaWriter,
		bundle.Publisher,
		reader,
		dispatcher,
		lifecycle,
		log,
	)

	// Asset lifecycle repositories: wired from the canonical
	// sqassets.AssetStoreSQLite (same DB handle bundle.DB.DB, same logger).
	// AssetProcRepo + AssetVerRepo are MANDATORY — called in
	// run_orchestrator_stages.go (Start/Fail/Complete + Append); nil would
	// panic on any real /run invocation (PRIORITÀ ASSOLUTA per la matrice
	// Frequenza×Complessità). AssetLocRepo is a free bonus (zero call sites
	// today but cheap to wire now).
	//
	// godlike/06 SSOT: the canonical adapter factories live on
	// *assets.AssetStoreSQLite.ProcessingRepository() / .VersionRepository() /
	// .LocationRepository() (processing_queries.go / version_queries.go /
	// location_queries.go).
	assetSQLiteStore := assets.NewAssetStoreSQLite(bundle.DB.DB, log)
	assetProcRepo := assetSQLiteStore.ProcessingRepository()
	assetVerRepo := assetSQLiteStore.VersionRepository()
	assetLocRepo := assetSQLiteStore.LocationRepository()

	// Stager (SourceStager port, Step 9/12): wraps the Artlist Downloader
	// so run_orchestrator_stages.go can use the canonical SourceStager
	// contract instead of falling through to the legacy mediaProcessor
	// pipeline.
	//
	// PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY (July 2026): wire the unified
	// Resolver instead of the legacy Provider. Resolver consolidates
	// all three transport paths (Node scraper / yt-dlp / HTTP) into a
	// single resolvePath decision — the duplicated isArtlistURL /
	// isDirectURL / isHLSURL detection in processor_download.go is
	// superseded by the canonical routing in resolver.go.
	//
	// godlike/06 SSOT: internal/infrastructure/artlist/downloader.Resolver
	// is the SINGLE canonical owner of Artlist download routing.
	// ResolverConfig{ScraperURL: cfg.External.ArtlistScraperServerURL}
	// feeds the Node scraper path; Config defaults (3 retries, 1s/30s
	// backoff, 0.3 jitter, 5m HTTP timeout, 5m scraper timeout) are the
	// same as the legacy Provider.
	//
	// ART-002 P1.1 (July 2026): wire the Prometheus metrics surface via
	// the 4th Pattern-0 arg. NewMetrics() returns a struct pointing at
	// the promauto global observability.ArtlistDownloadPathTotal
	// (auto-registered with prometheus.DefaultRegisterer + surfaced via
	// /metrics). All 4 path labels (PathBrowser / PathYTDLP / PathHTTP /
	// PathHLS) are now fired by the unified resolver.
	artlistDownloader := downloader.NewResolver(cfg, downloader.ResolverConfig{
		ScraperURL: cfg.External.ArtlistScraperServerURL,
	}, log, downloader.NewMetrics())
	// Compile-time pin lives in the infra package.
	_ = (artlistPkg.Downloader)(artlistDownloader)
	artlistStager := artlistPkg.NewArtlistStager(artlistDownloader)

	// PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY-CUTOVER (July 2026): inject
	// the Resolver into the media processor's ArtlistDownloader port
	// so downloadStep routes Artlist clips through the canonical
	// Resolver instead of the legacy downloadViaScraper method.
	// The adapter is the SINGLE translation site between the
	// processor's narrow ArtlistDownloader interface and the
	// Resolver's Download(artlist.DownloadRequest) method.
	if bundle.MediaProcessor != nil {
		if mp, ok := bundle.MediaProcessor.(*mediaproc.Processor); ok {
			mp.SetArtlistDownloader(&artlistProcessorDownloadAdapter{resolver: artlistDownloader})
			log.Info("WireArtlist: ArtlistDownloader wired into media processor (Resolver bridge)")
		}
	}

	// 19-field ServiceDeps literal via nested named-struct init (8 ServicePorts
	// + 11 ServiceDependencies). 3 forward-pointer nil fields tagged with
	// linked_issue id per architecture/current.yaml#ART-001.linked_issues
	// (PR-ARTLIST-SEARCHERS closed 2026-07-04: 3 searchers wired inline).
	service, err := artlistPkg.NewService(artlistPkg.ServiceDeps{
		ServicePorts: artlistPkg.ServicePorts{
			// ServicePorts (9) — 9 DIRECT (PR-ARTLIST-SEARCHERS closed: 3 searchers
			// constructed inline from cfg + the canonical infra concretes; runtime
			// returns ErrUnavailable when API keys are empty per godlike/07 graceful
			// degradation, instead of nil-tolerated 503 at the handler layer).
			AssetStore:     bundle.ClipsRepo,
			Indexer:        bundle.ClipIndexerService,
			MetadataWriter: semanticEnricher,
			Publisher:      bundle.Publisher,
			ScraperSearcher: scraper.New(scraper.Config{
				ServerURL:  cfg.External.ArtlistScraperServerURL,
				ScraperDir: cfg.External.NodeScraperDir,
				ScriptName: "artlist_search.js",
			}, log),
			PixabaySearcher: fallback.NewPixabay(fallback.Config{
				APIKey:     cfg.External.PixabayAPIKey,
				BaseURL:    cfg.External.PixabayBaseURL,
				SourceName: "pixabay",
			}),
			PexelsSearcher: fallback.NewPexels(fallback.Config{
				APIKey:     cfg.External.PexelsAPIKey,
				BaseURL:    cfg.External.PexelsBaseURL,
				SourceName: "pexels",
			}),
			Stager:      artlistStager,
			IsLiveProbe: isLiveProbe,
		},
		ServiceDependencies: artlistPkg.ServiceDependencies{
			// ServiceDependencies (10) — 10 DIRECT.
			Cfg:               cfg,
			MainDB:            bundle.DB.DB,
			Log:               log,
			Dispatcher:        dispatcher,
			MediaProcessor:    bundle.MediaProcessor,
			AssetDestResolver: destResolver,
			JobsSvc:           bundle.Jobs.Service,
			AssetProcRepo:     assetProcRepo,
			AssetVerRepo:      assetVerRepo,
			AssetLocRepo:      assetLocRepo,
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
	bundle.ClipResolver = NewClipResolverRecommendAdapter(
		scripts_usecase.NewClipResolver(bundle.ClipsRepo, log),
		log,
	)

	descriptor, err := artlistapi.Build(artlistapi.Dependencies{
		Service:     service,
		CatalogSync: bundle.CatalogSyncService,
		Jobs:        bundle.Jobs.Service,
		// PR-ARTLIST-RECOMMEND-ADAPTER (closed 2026-07-04): now
		// WIRED via the new clipResolverRecommendAdapter (built
		// above) — no longer an unset forward-pointer. /recommend
		// returns real recommendations when canonical is available.
		ClipResolver:   bundle.ClipResolver,
		NodeScraperDir: cfg.External.NodeScraperDir,
		CfgPort:        newArtlistConfigAdapter(cfg),
		EnabledFunc:    func() bool { return cfg.Features.ArtlistEnabled },
		ModuleOpts:     nil, // forward-pointer: PR-COMPOSITION-MODULE-OPTS
		Logger:         log,
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

	log.Info("WireArtlist: ART-001 reversal wiring complete",
		zap.String("descriptor_name", ad.Name()),
		zap.Bool("godlike_06_ssot", true),
	)
	return &ArtlistWiring{Module: ad.Module, Service: ad.Service}, nil
}

// artlistProcessorDownloadAdapter bridges the processor's narrow
// ArtlistDownloader interface to the canonical downloader.Resolver.
// SINGLE translation site per godlike/06 SSOT.
type artlistProcessorDownloadAdapter struct {
	resolver *downloader.Resolver
}

func (a *artlistProcessorDownloadAdapter) DownloadArtlistClip(
	ctx context.Context, sourceURL, clipPageURL, clipID, destDir, filename string,
) (string, error) {
	result, err := a.resolver.Download(ctx, artlistPkg.DownloadRequest{
		SourceRef:     sourceURL,
		ClipPageURL:   clipPageURL,
		ClipID:        clipID,
		DestinationID: destDir,
		Filename:      filename,
	})
	if err != nil {
		return "", err
	}
	return result.LocalPath, nil
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
//	(b) Configure scraper:  ARTLIST_SCRAPER_SERVER_URL=http://artlist-scraper:9123
//	                        (docker-compose.yml production setup)
func validateArtlistScraperURL(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("WireArtlist: cfg is nil (gate #5 scraper-URL fail-closed cannot evaluate)")
	}
	if cfg.Features.ArtlistEnabled && cfg.External.ArtlistScraperServerURL == "" {
		return fmt.Errorf("WireArtlist: cfg.Features.ArtlistEnabled=true but cfg.External.ArtlistScraperServerURL is empty (ART-002 P0.1 fail-closed; required env ARTLIST_SCRAPER_SERVER_URL — without it the searcher chain silently degrades to per-call exec fallback). To disable Artlist set VELOX_FEATURE_ARTLIST_ENABLED=false")
	}
	return nil
}

// WireArtlistJobBindings registers the Artlist job handler with the jobs dispatcher.
// Extracted from WireArtlist so the late-binding has a dedicated composition surface
// (mirrors wireYoutubeCatalogJobBindings precedent in build_bundles_youtube.go).
func WireArtlistJobBindings(artlistSvc *artlistPkg.Service, jobsBundle *JobsBundle) error {
	if artlistSvc == nil {
		return fmt.Errorf("WireArtlistJobBindings: artlistSvc is nil")
	}
	if jobsBundle == nil || jobsBundle.Service == nil {
		return fmt.Errorf("WireArtlistJobBindings: jobsBundle.Service is nil")
	}
	return artlistSvc.RegisterHandler(jobsBundle.Service)
}
