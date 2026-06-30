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
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
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
	_, _, searchAgg := registerSearchBackend(log, providerReg, root.Repos.ClipsRepo, wiring)
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
	mediaIngestW, mediaIngestErr := WireMediaIngest(cfg, log, &MediaIngestBundle{
		DB:                root.DB,
		Assets:            root.Repos.Assets,
		DriveUploader:     root.Drive.driveUploader,
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

	// Step 7 — FullImages (bundle-driven; ImageService + MediaStore).
	fullImagesW, fullImagesErr := WireFullImages(cfg, log, root.Domains.ImageService, root.Drive.MediaStore)
	wiring.FullImages = fullImagesW
	if fullImagesErr != nil {
		log.Warn("failed to wire module", zap.String("module", "FullImages"), zap.Error(fullImagesErr))
	} else if fullImagesW != nil && fullImagesW.Module != nil {
		if err := tryRegisterModuleStrict(registry, log, fullImagesW.Module, WithRegistrationPoint("register.FullImages")); err != nil {
			return fmt.Errorf("wire registry: full-images: %w", err)
		}
	}

	// Step 8 — StockPipeline (bundle-driven).
	stockW, stockErr := WireStockPipeline(cfg, log, &StockBundle{
		DriveUploader:      root.Drive.driveUploader,
		Jobs:               root.Jobs.Service,
		JobFacade:          root.Jobs.Facade,
		AssetIndexService:  root.Search.AssetIndexService,
		ClipsRepo:          root.Repos.ClipsRepo,
		YoutubeClipService: root.Domains.YoutubeClipService,
		ClipIndexerService: root.Process.ClipIndexerService,
		Dispatcher:         root.Outbox.Dispatcher,
		Publisher:          root.Drive.Publisher,
	})
	wiring.StockPipeline = stockW
	if stockErr != nil {
		log.Warn("failed to wire module", zap.String("module", "StockPipeline"), zap.Error(stockErr))
	} else if stockW != nil && stockW.Module != nil {
		if err := tryRegisterModuleStrict(registry, log, stockW.Module, WithRegistrationPoint("register.StockPipeline")); err != nil {
			return fmt.Errorf("wire registry: stock-pipeline: %w", err)
		}
	}

	_ = ctx // unused at this level; consumers (Artlist, ScriptFlow) use it
	return nil
}

// registerArtlist wires the Artlist module.
func registerArtlist(ctx context.Context, registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, wiring *RegistryWiring) error {
	artlistBundle := &ArtlistBundle{
		DB:                 root.DB,
		Assets:             root.Repos.Assets,
		ClipsRepo:          root.Repos.ClipsRepo,
		DriveUploader:      root.Drive.driveUploader,
		DriveClient:        root.Drive.DriveClient,
		AssetIndexService:  root.Search.AssetIndexService,
		ClipIndexerService: root.Process.ClipIndexerService,
		MediaProcessor:     root.Process.MediaProcessor,
		Jobs:               root.Jobs,
		CatalogSyncService: root.Sync.CatalogSync,
	}
	aw, err := WireArtlist(ctx, cfg, log, artlistBundle, root.Outbox.Dispatcher, root.Drive.Publisher)
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "Artlist"), zap.Error(err))
		return nil
	}
	if err := tryRegisterModuleStrict(registry, log, aw.Module, WithRegistrationPoint("register.Artlist")); err != nil {
		return fmt.Errorf("wire registry: artlist: %w", err)
	}
	wiring.ArtlistSvc = aw
	return nil
}

// registerYouTubeClip wires the YouTubeClip module.
func registerYouTubeClip(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot, wiring *RegistryWiring, searchAgg *providers.SearchAggregator) error {
	yw, err := WireYouTubeClip(cfg, log, root.Domains.YoutubeClipService, root.Jobs.Facade, root.Jobs.Service, root.Repos.ClipsRepo, toolCheckerAdapter, wiring.idempotencyHandler, searchAgg)
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "YouTubeClip"), zap.Error(err))
		return nil
	}
	if err := tryRegisterModuleStrict(registry, log, yw.Module, WithRegistrationPoint("register.YouTubeClip")); err != nil {
		return fmt.Errorf("wire registry: youtube: %w", err)
	}
	wiring.YouTubeClip = yw
	return nil
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
		Stats:       root.Jobs.Service, // *appjobs.Service satisfies both domainjob.Service + appjobs.JobStatsReader
		EnabledFunc: func() bool { return true }, // jobs is always on in production
		ModuleOpts:  nil,                          // no per-feature middleware (matches pre-Step-13 wiring)
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
