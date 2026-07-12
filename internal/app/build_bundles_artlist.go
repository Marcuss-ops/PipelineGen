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
// discipline). The 2 repo fields (AssetProcRepo / AssetVerRepo)
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
	"errors"
	"fmt"

	artlistapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/artlist"
	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets/adapters"
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
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
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
	metaWriter semantic.MetadataWriterPort,
	destResolver asset.Resolver,
) (*ArtlistWiring, error) {
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

	// jobdomain.Service alias pin is verified at compile time (Pattern 0 + AGENTS.md).

	// gate #7 — Finalizer (canonical transactional AssetTxFinalizer).
	// Per user spec literal "finalizer" listing + per godlike/07
	// fail-closed: the constructor's nil-discard path is contractually
	// permitted (a future config-validation branch could return nil
	// on incompatible schema). Composition-time rejection via typed
	// sentinel pins the invariant; `Field: "finalizerTx"` so the
	// diagnostic surfaces the source-path beside the well-known Kind.
	finalizerTx := assetfinalizer.NewAssetTxFinalizer(log)
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
	_ = (jobdomain.Service)(bundle.Jobs.Service)

	log.Info("WireArtlist: ART-001 reversal wiring starting",
		zap.String("root_path", "/api/artlist/*"),
		zap.Bool("godlike_07_fail_closed", true),
	)

	// PR-HLS-AES128 followup-2 (July 2026): construct the canonical ffprobe
	// Processor ONCE at composition scope (lifted out of the closure to
	// avoid per-call allocation, per code-reviewer nit-1). The Processor
	// holds only the ffmpeg-derived path; Probe runs the binary as a
	// subprocess. Fail-closed: missing ffprobe binary yields a typed
	// exec error from process.Run that the closure below forwards to
	// markAudit(Failed) + ErrInvalidResponse to the caller.
	ffmpegProc := ffmpeg.NewProcessor("")

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
	// semantic.MetadataWriterPort; its Enrich(ctx, clip, term) signature matches
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
	// Frequenza×Complessità).
	//
	// PR-DEADC-ARTLIST-ASSET-LOC-REPO-RETIRE (2026-07-10): AssetLocRepo
	// retired per `architecture/action-plans/2026-07-10-dead-code-p1-p2-cleanup.md#§3-Phase-C`.
	// assetSQLiteStore.LocationRepository() remains available for future
	// callers but is no longer wired into the artlist service (zero
	// call sites in service.go after retirement). godlike/06 SSOT:
	// the canonical adapter factories still live on
	// *assets.AssetStoreSQLite.ProcessingRepository() / .VersionRepository()
	// (processing_queries.go / version_queries.go).
	assetSQLiteStore := assets.NewAssetStoreSQLite(bundle.DB.DB, log)
	assetProcRepo := assetSQLiteStore.ProcessingRepository()
	assetVerRepo := assetSQLiteStore.VersionRepository()

	// PR-ARTLIST-PERSIST-FIX (2026-07-04): mandatory RunRepository
	// wiring (godlike/07 fail-closed) via the composition-root
	// adapter. NewArtlistRunsRepository is the canonical SOLE
	// writer of artlist_runs rows; its absence makes /api/artlist/run
	// return fake-success (the original bug). The concrete is
	// constructed here and wrapped in the canonical
	// artlistRunsRepoAdapter (internal/app/artlist_runs_adapter.go)
	// which holds the SINGLE compile-time pin to
	// artlist.RunRepository (mirrors the ClipsRepository precedent:
	// no cycle, adapter in composition root).
	artlistRunsRepo, err := assets.NewArtlistRunsRepository(bundle.DB.DB, log)
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: NewArtlistRunsRepository: %w", err)
	}
	artlistRunsAdapter := NewArtlistRunsRepoAdapter(artlistRunsRepo)
	_ = (artlistPkg.RunRepository)(artlistRunsAdapter) // compile-time pin surface

	// P0 (July 2026): download audit repository for rate-limit and
	// compliance tracking. Constructed from the same media.db.sqlite
	// handle and bridged to the artlist port via the composition-root
	// adapter.
	artlistDownloadAuditRepo, err := assets.NewArtlistDownloadAuditRepository(bundle.DB.DB, log)
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: NewArtlistDownloadAuditRepository: %w", err)
	}
	artlistDownloadAuditAdapter := NewArtlistDownloadAuditAdapter(artlistDownloadAuditRepo)
	_ = (artlistPkg.DownloadAuditRepository)(artlistDownloadAuditAdapter) // compile-time pin surface

	// License, release and rendition compliance repositories. They share the
	// same media.db.sqlite handle and are exposed on ArtlistWiring for use by
	// handlers and audit tooling.
	licenseRepo, err := assets.NewAssetLicenseRepository(bundle.DB.DB, log)
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: NewAssetLicenseRepository: %w", err)
	}
	releaseRepo, err := assets.NewAssetReleaseRepository(bundle.DB.DB, log)
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: NewAssetReleaseRepository: %w", err)
	}
	renditionRepo, err := assets.NewAssetRenditionRepository(bundle.DB.DB, log)
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: NewAssetRenditionRepository: %w", err)
	}

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
		ScraperURL:         cfg.External.ArtlistScraperServerURL,
		AcquisitionMode:    artlistPkg.ArtlistAcquisitionMode(cfg.External.ArtlistAcquisitionMode),
		AccountID:          cfg.External.ArtlistAccountID,
		DailyDownloadLimit: cfg.External.ArtlistDailyDownloadLimit,
		AuditRepository:    artlistDownloadAuditAdapter,
		// PR-HLS-AES128 (P1, July 2026): wire the canonical ffprobe
		// post-validator. Every authorized_api download is now ffprobe-
		// sanity-checked BEFORE markAudit(Succeeded) can fire
		// (godlike/07 no-fake-availability). The wrapped Probe returns
		// (*MediaInfo, error); we coerce to (ctx, path) error so the
		// ResolverConfig.PostValidator signature stays clean. The
		// HasVideo gate is the spec-required "file is readable" check:
		// HasVideo=false means the bytes on disk don't look like a real
		// media file (corrupt transport, missing AES-128 stage in
		// upstream Node scraper, etc.) and the audit row MUST flip to
		// Failed instead of silently succeeding.
		//
		// Fail-closed: if ffprobe is missing on PATH, ffmpeg.Processor.Probe
		// returns an error (from process.Run's exec.LookPath), and the
		// Resolver correctly routes this to markAudit(Failed) + a typed
		// ErrInvalidResponse to the caller. Operationally: the operator
		// sees the audit-status flip and an actionable log line naming
		// ffprobe as the missing dependency.
		PostValidator: func(ctx context.Context, path string) error {
			mediaInfo, err := ffmpegProc.Probe(ctx, path)
			if err != nil {
				return err
			}
			// PR-HLS-AES128 followup-2 (reviewer nit-2): accept audio-only
			// downloads as well as video — the spec says "file is readable"
			// which a valid audio stream satisfies. The underlying Probe()
			// error path still catches corrupt containers / unparseable files
			// (godlike/07 fail-closed).
			if !mediaInfo.HasVideo && !mediaInfo.HasAudio {
				return fmt.Errorf("ffprobe: no media stream detected in %q (corrupt container or missing AES-128 stage upstream)", path)
			}
			return nil
		},
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

	// PR-ARTLIST-SEARCHERS (2026-07-04): construct the public-stock
	// searchers once and reuse them both for the legacy fallback chain
	// inside artlist.Service and for the new unified providerassets
	// registry. This avoids duplicate HTTP clients and keeps the two
	// surfaces observationally equivalent.
	pexelsSearcher := fallback.NewPexels(fallback.Config{
		APIKey:     cfg.External.PexelsAPIKey,
		BaseURL:    cfg.External.PexelsBaseURL,
		SourceName: "pexels",
	})
	pixabaySearcher := fallback.NewPixabay(fallback.Config{
		APIKey:     cfg.External.PixabayAPIKey,
		BaseURL:    cfg.External.PixabayBaseURL,
		SourceName: "pixabay",
	})

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
			PixabaySearcher: pixabaySearcher,
			PexelsSearcher:  pexelsSearcher,
			Stager:          artlistStager,
			IsLiveProbe:     isLiveProbe,
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
			RunRepository:  artlistRunsAdapter,
			SearchStrategy: artlistPkg.ArtlistSearchStrategy(cfg.External.ArtlistSearchStrategy),
		},
		ServiceDependencies: artlistPkg.ServiceDependencies{
			// ServiceDependencies (10) — 10 DIRECT.
			Cfg:               cfg,
			Log:               log,
			MainDB:            bundle.DB.DB,
			Dispatcher:        dispatcher,
			MediaProcessor:    bundle.MediaProcessor,
			AssetDestResolver: destResolver,
			JobsSvc:           bundle.Jobs.Service,
			AssetProcRepo:     assetProcRepo,
			AssetVerRepo:      assetVerRepo,
			// PR-ARTLIST-FINALIZER (July 2026): canonical transactional
			// asset finalizer. Replaces the legacy dispatchBridge path.
			// Phase 1 (Fase 1, July 2026): finalizerTx is declared above
			// (gate #7) so the typed sentinel path is the SOLE
			// canonical owner of the fail-closed check; the previous
			// inline-construction shape masked the nil-discard path.
			AssetFinalizerTx: finalizerTx,
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

	// ProviderAssets registry: unified catalog surface for Artlist,
	// Pexels and Pixabay. The registry is frozen before the module
	// is returned so no provider can be added or removed at runtime.
	providerAssetsRegistry := providerassets.NewRegistry()
	artlistAdapter := adapters.NewSearchProviderAdapter("artlist", artlistPkg.NewAdapter(service))
	_ = providerAssetsRegistry.Register(artlistAdapter)
	_ = providerAssetsRegistry.Register(adapters.NewSearcherAdapter("pexels", pexelsSearcher))
	_ = providerAssetsRegistry.Register(adapters.NewSearcherAdapter("pixabay", pixabaySearcher))
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
		LicenseRepo:    licenseRepo,
		ReleaseRepo:    releaseRepo,
		RenditionRepo:  renditionRepo,
	}, nil
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
//	(b) Configure scraper:  VELOX_ARTLIST_SCRAPER_SERVER_URL=http://artlist-scraper:9123
//	                        (docker-compose.yml production setup, PR-ARTLIST-CONFIG-PREFIX
//	                        July 2026 renamed from the bare ARTLIST_SCRAPER_SERVER_URL)
// validateArtlistScraperURL is the canonical fail-closed gate #5
// (ART-002 P0.1, July 2026) for WireArtlist. Returns the typed
// ErrArtlistDepMissing sentinel when the Artlist feature is enabled
// but the Node scraper server URL is empty (or cfg itself is nil).
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
// Escape hatches (kept in the Detail field for the operator-fix hint):
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
			Kind:  DepKindScraperURL,
			Field: "cfg.External.ArtlistScraperServerURL",
			Detail: "ART-002 P0.1: cfg.Features.ArtlistEnabled=true but cfg.External.ArtlistScraperServerURL is empty — required env VELOX_ARTLIST_SCRAPER_SERVER_URL (without it the searcher chain silently degrades to per-call exec fallback). To disable Artlist set VELOX_FEATURE_ARTLIST_ENABLED=false",
		}
	}
	return nil
}

// ErrArtlistConsumerRegistrationFailed is the typed sentinel the
// composition caller (registerArtlist) reads to abort boot when the
// Artlist job handler fails to bind to the jobs dispatcher. The
// sentinel wraps the underlying RegisterHandler error so operator
// log-lines and tests can branch on intent (godlike/06 SSOT).
//
// PR-P2-FAILCLOSED-JOB (July 2026): the previous wire-bond step
// silently log.Warn'd + continued (a godlike/07 fake-availability
// violation — media.artlist jobs would have queued to dead-letter
// forever). The composition caller MUST abort on this error rather
// than mask it; defining the sentinel here keeps the SSOT single-
// source for both the gate error and the abort-contract test.
var ErrArtlistConsumerRegistrationFailed = errors.New("artlist: consumer-job registration failed at composition — production must abort boot (godlike/07 no-fake-availability)")

// ════════════════════════════════════════════════════════════════════
//  ErrArtlistDepMissing — typed per-dep fail-closed sentinel (Fase 1)
//
//  godlike/06 SSOT: this file is the SINGLE canonical owner of the
//  typed sentinel + DepKind constant set. Every WireArtlist mandatory
//  gate returns an instance so orchestrators (registerArtlist) can:
//   1. Branch on intent via `errors.As(err, &missing)` — structured logs
//      (zap.String("missing_dep", missing.Kind.String())).
//   2. Surface the missing field name (Field) verbatim so operators can
//      map to the upstream ComposeRoot / runtime receipt.
//   3. Avoid the godlike/07 fake-availability anti-pattern of
//      `log.Warn + skip-route + return-nil` (previous behavior) — the
//      composition caller now aborts boot with a typed-wrapped error.
//
//  Phase 1 (DoD §1) maps 6 of the 10 user-listed deps to hard gates.
//  Indexer (Qdrant indexer) / FFmpeg processor / Downloader are
//  intentionally NOT gated by design: their prod-bit-state is verified
//  at runtime via the canonical PostValidator + IsLiveProbe + ffprobe
//  binary-detection paths surfaced via WireArtlist's composition-time
//  construction (NOT composition-time fail-closed). A Fase 1.5 follow-
//  up may promote them to typed gates if the operator-only battery
//  requires composition-time visibility — for now per-dep telemetry is
//  surfaced via /api/artlist/diagnostics (Fase 2 follow-up).
// ════════════════════════════════════════════════════════════════════

// DepKind enumerates the canonical Artlist composition dependency
// kinds. The string value is the canonical log/diagnostic tag — tests
// branch on errors.As depth matching; operators grep on these strings.
type DepKind string

const (
	// DepKindRunRepo gates `bundle == nil` (the ArtlistBundle itself).
	DepKindRunRepo DepKind = "ArtlistBundle"
	// DepKindPublisher gates `bundle.Publisher == nil` (canonical delivery.Publisher).
	DepKindPublisher DepKind = "DrivePublisher"
	// DepKindDispatcher gates `dispatcher == nil` (canonical outbox.Dispatcher).
	DepKindDispatcher DepKind = "OutboxDispatcher"
	// DepKindClipsRepo gates `bundle.ClipsRepo == nil` (canonical *assets.ClipsRepository).
	DepKindClipsRepo DepKind = "ClipsRepository"
	// DepKindJobsService gates `bundle.Jobs.Service == nil` (composition-time JobsBundle.Service).
	DepKindJobsService DepKind = "JobsService"
	// DepKindScraperURL gates the (cfg.Features.ArtlistEnabled &&
	// cfg.External.ArtlistScraperServerURL=="") pair via validateArtlistScraperURL.
	DepKindScraperURL DepKind = "ArtlistScraperServerURL"
	// DepKindIndexer gates `bundle.ClipIndexerService == nil` (Qdrant clipindexer port).
	// The pre-Fase-1 service thread would silently nil-deref at first
	// outbox dispatch — composition-time rejection turns a runtime 500
	// into a typed boot abort.
	DepKindIndexer DepKind = "ClipIndexerService"
	// DepKindFinalizer gates the assetfinalizer.NewAssetTxFinalizer(log)
	// nil-discard path. The constructor today always returns non-nil;
	// the gate pins the contract at composition time so a future
	// implementation that conditionally returns nil (e.g., early
	// config-validation failure) cannot silently regress the
	// fail-closed invariant.
	DepKindFinalizer DepKind = "AssetTxFinalizer"
)

// String makes DepKind satisfy fmt.Stringer so zap.String fields
// (zap.String("missing_dep", missing.Kind.String())) render cleanly
// without explicit casts.
func (k DepKind) String() string { return string(k) }

// ErrArtlistDepMissing is the typed per-dep sentinel WireArtlist
// returns at every mandatory gate. errors.As(err, &missing) lets
// orchestrators programmatically branch on the missing dep. The Detail
// field optionally carries the original verbose message (scraper-URL
// gate uses it to retain the operator env-var hint verbatim; simple
// gates leave it empty).
type ErrArtlistDepMissing struct {
	Kind   DepKind
	Field  string
	Detail string
}

// Error satisfies the error interface; the format is greedy-named so
// operators grepping for `mandatory dependency missing:` land on the
// canonical diagnostic string. Detail (when non-empty) is appended
// after the godlike/07 marker for the operator-fix hint paths.
func (e ErrArtlistDepMissing) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("artlist: mandatory dependency missing: %s (field: %s) — godlike/07 fail-closed; %s", e.Kind, e.Field, e.Detail)
	}
	return fmt.Sprintf("artlist: mandatory dependency missing: %s (field: %s) — godlike/07 fail-closed", e.Kind, e.Field)
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
func WireArtlistJobBindings(artlistSvc *artlistPkg.Service, jobsBundle *JobsBundle) error {
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
	if !jobsBundle.Service.HasHandler(jobdomain.TypeArtlistRun) {
		return fmt.Errorf("%w: post-bind HasHandler(media.artlist) returned false (dispatcher silently dropped the Register call?)",
			ErrArtlistConsumerRegistrationFailed)
	}
	return nil
}
