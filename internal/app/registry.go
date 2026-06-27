// Package app — registry composition (PR4d-final: takes *ComposeRoot).
//
// PR4d-final (June 2026): WireRegistry takes ONLY *ComposeRoot + ctx.
// The legacy *CoreDeps projection was deleted; all reads inside WireRegistry
// (the ScriptFlow inline block, the late-bindings, the channels/content/
// search-queries/utility module registrations) now source from
// root.<bundle>.<field> directly.
//
// Body is structurally identical to pre-PR4d: build RegistryWiring,
// late-inject ImageService → MediaIngest Service, mutate
// ProviderRegistry.Freeze() at the very end of WireRegistry (Reviewer Q8 fix).
package app

import (
	"context"
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	imagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/images"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	outboxapi "github.com/Marcuss-ops/PipelineGen/internal/api/outbox"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	artlistadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	stockadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock"
	youtubeadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/youtube"
	searchqueriesuc "github.com/Marcuss-ops/PipelineGen/internal/application/assets/searchqueries"
	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptassets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"

	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/api/mediasearch"
	generation "github.com/Marcuss-ops/PipelineGen/internal/application/generation"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// RegistryWiring holds the registry and all wired modules.
// PR2 (June 2026): removed System, Jobs, Images, Drive, Scraper — those were
// thin Wire wrappers inlined directly in WireRegistry below.
type RegistryWiring struct {
	Registry      *module.Registry
	ArtlistSvc    *ArtlistWiring
	YouTubeClip   *YouTubeClipWiring
	MediaIngest   *MediaIngestWiring
	Assets        *AssetsWiring
	FullImages    *FullImagesWiring
	StockPipeline *StockPipelineWiring

	// QDRANT-002 + QDRANT-004 separation-of-routes (June 2026):
	// These handlers are constructed by WireRegistry but NOT registered
	// in the public /api registry. They are plumbed through AppDeps and
	// mounted on the /internal/v1 WorkerAuth-protected internalGroup
	// by cmd/server/main.go. The split is enforced by the
	// anti-regression test internal/api/routes_test.go.
	OutboxHandler      interface{ RegisterRoutes(*gin.RouterGroup) }
	MediasearchHandler interface{ RegisterRoutes(*gin.RouterGroup) }
}

// strictOption is the composition-site metadata tag passed to
// tryRegisterModuleStrict via WithRegistrationPoint. The tag is
// surfaced in error messages so an operator can pin the exact
// WireRegistry block responsible for a duplicate/register/freeze
// failure. Composition sites that omit the tag default to "unknown".
type strictOption func(*strictRegCtx)

type strictRegCtx struct {
	point string
}

// WithRegistrationPoint tags the next tryRegisterModuleStrict call with
// the composition site in WireRegistry that issued it (e.g.,
// "register.Generation", "register.Assets"). The tag is surfaced in
// error messages so an operator can pin the exact call site that
// emitted a duplicate or freeze failure. Composition sites that don't
// tag default to "unknown".
func WithRegistrationPoint(point string) strictOption {
	return func(c *strictRegCtx) {
		if point != "" {
			c.point = point
		}
	}
}

func collectRegPoint(opts []strictOption) string {
	var c strictRegCtx
	for _, o := range opts {
		if o != nil {
			o(&c)
		}
	}
	if c.point == "" {
		return "unknown"
	}
	return c.point
}

// tryRegisterModuleStrict is the composition-time registration path.
// It is the ONLY composition-time helper for publishing a Module into
// the api.Registry; the previous permissive tryRegisterModule was
// deleted in PR 1 (commit 81e79728) so duplicate module publication
// surfaces as a hard error instead of silently dropping the duplicate
// on a Debug log.
//
// Cross-slot publication (DescriptorJobs / DescriptorProviders
// publishing the same capability name through a shared Descriptor)
// registers to DISTINCT registries (module.Registry vs Jobs.Service
// vs providers.Registry), so the strict path is safe.
//
// PR 2 (June 2026 — codex/registry-strict-uniqueness) invariant set:
//   - nil module → explicit error (was NPE before PR 2).
//   - empty module name → explicit error ("module name is empty").
//   - post-freeze → existing sentinel ("registry is frozen").
//   - same instance, same name → silent no-op (composition-time
//     idempotency; PR 2 contract pinned by
//     TestRegisterSameInstanceMultipleSlots_NoError).
//   - different instance, same name → explicit error ("already registered").
//
// The composed error carries three composition-level fields required by
// the branch spec ("Inserire nel messaggio: nome capability; tipo
// descriptor; punto di registrazione"):
//
//	compose: capability=%q, descriptor-type=%T, registration-point=%s: <inner>
//
// The "compose:" prefix is pinned by
// TestTryRegisterModule_ErrorContainsSpecMarker in
// internal/app/registry_failfast_test.go; do not change without updating
// the test marker.
// tryRegisterModule is the production-path register helper retained
// for fail-fast regression coverage (registry_failfast_test.go tests
// "production path fails on duplicate"). Delegates to the strict
// variant so all register paths share one composition-time failure
// rule; the PR 1 (June 2026) coalescing Has-check deletion is the
// reason this is now a thin one-line passthrough (per the test's
// own spec marker "After the composition-fix (June 2026)...").
func tryRegisterModule(registry *module.Registry, log *zap.Logger, mod module.Module) error {
	return tryRegisterModuleStrict(registry, log, mod)
}

func tryRegisterModuleStrict(registry *module.Registry, log *zap.Logger, mod module.Module, opts ...strictOption) error {
	if registry == nil {
		// Composition-bug guard: a nil registry is never expected at
		// composition time. Surface the bug here so WireRegistry fails
		// fast with a clear operator message.
		return fmt.Errorf("compose: nil api.Registry passed to strict-register (registration-point=%s)", collectRegPoint(opts))
	}
	if mod == nil {
		return fmt.Errorf("compose: nil module passed (registration-point=%s)", collectRegPoint(opts))
	}
	if err := registry.Register(mod); err != nil {
		if log != nil {
			log.Warn("strict-register failed",
				zap.String("module", mod.Name()),
				zap.String("registration-point", collectRegPoint(opts)),
				zap.Error(err))
		}
		// Pin "compose:" prefix; spec fields = capability + descriptor-type +
		// registration-point. Inner %w preserves the sentinel substrings
		// pinned by failfast tests ("already registered", "frozen",
		// "module name is empty").
		return fmt.Errorf("compose: capability=%q, descriptor-type=%T, registration-point=%s: %w",
			mod.Name(), mod, collectRegPoint(opts), err)
	}
	return nil
}

// WireRegistry creates and populates the module registry with all modules.
//
// PR4d-final (June 2026): signature takes (ctx, cfg, log, root). The
// transitional cd parameter was removed. All reads source from root.<bundle>.
func WireRegistry(ctx context.Context, cfg *config.Config, log *zap.Logger, root *ComposeRoot) (*RegistryWiring, error) {
	if root == nil {
		return nil, fmt.Errorf("wire registry: compose root is nil")
	}

	registry := module.NewRegistry()
	wiring := &RegistryWiring{Registry: registry}

	// System module — no deps (PR2: inlined from WireSystem).
	// PR3 (June 2026): Wave 14 close — the system module absorbed the
	// former `internal/api/drive/` directory as a second receiver
	// (DriveHandler) sharing the same /drive sub-group. The ctor takes
	// driveUploader + reconcileSvc so /drive routes can answer (when
	// either is nil the corresponding handler returns 503).
	var driveUploaderAdapter *driveup.Uploader
	if root.Drive != nil && root.Drive.DriveClient != nil {
		driveUploaderAdapter = &driveup.Uploader{Service: root.Drive.DriveClient, Log: log}
	}
	if err := tryRegisterModuleStrict(registry, log, systemapi.NewModule(
		doctorConfigFrom(cfg),
		log,
		toolCheckerAdapter, processRunnerAdapter, dbHealthCheckerAdapter,
		newDriveAdminAdapter(driveUploaderAdapter, log),
		&noopReconciler{},
	), WithRegistrationPoint("register.System")); err != nil {
		return nil, fmt.Errorf("wire registry: system module: %w", err)
	}

	// Artlist (PR4d-chunk2): takes *ArtlistBundle + vectorStore.
	artlistBundle := &ArtlistBundle{
		DB:                 root.DB,
		Assets:             root.Repos.Assets,
		ClipsRepo:          root.Repos.ClipsRepo,
		DriveClient:        root.Drive.DriveClient,
		DriveUploader:      root.Drive.DriveUploader,
		AssetIndexService:  root.Search.AssetIndexService,
		ClipIndexerService: root.Process.ClipIndexerService,
		MediaProcessor:     root.Process.MediaProcessor,
		Jobs:               root.Jobs,
		CatalogSyncService: root.Sync.CatalogSync,
	}
	if aw, err := WireArtlist(ctx, cfg, log, artlistBundle, root.Outbox.Dispatcher); err != nil {
		log.Warn("failed to wire module", zap.String("module", "Artlist"), zap.Error(err))
	} else {
		if err := tryRegisterModuleStrict(registry, log, aw.Module, WithRegistrationPoint("register.Artlist")); err != nil {
			return nil, fmt.Errorf("wire registry: artlist: %w", err)
		}
		wiring.ArtlistSvc = aw
	}

	// ScriptFlow — sources from root.<bundle>.<field>. Extracted into
	// wireScriptFlow (PR7 cleanup, June 2026) to shrink WireRegistry and
	// reuse the canonical engine + memorySvc from AIBundle.
	if err := wireScriptFlow(ctx, cfg, log, root, registry); err != nil {
		return nil, fmt.Errorf("wire registry: script-flow: %w", err)
	}

	// YouTubeClip (PR4d-chunk2): 4 direct narrow args + ProviderRegistry.
	// ProviderRegistry is not yet populated when WireYouTubeClip runs —
	// the handler's constructor resolves providers lazily so it's fine
	// to pass the empty registry here; it will be populated by the time
	// HTTP requests arrive.
	// PR8 (June 2026): IdempPlus constructs the canonical reusable Gin
	// idempotency middleware instance from RepoBundle.IdempotencyStore;
	// shared across YouTubeClip, MediaIngest, and (via AssetsBundle)
	// clips + register handlers.
	idemPlus := middleware.NewIdempotency(root.Repos.IdempotencyStore, log)
	idemHandler := idemPlus.Handler()

	// PR-2 (June 2026): single canonical SearchFanOut constructed
	// in WireRegistry and SHARED between WireYouTubeClip +
	// WireAssets. The pre-PR-2 parallel
	// `providers.NewSearchAggregator` is gone (git-rm'd in this
	// PR); the canonical *search.Aggregator lives behind the
	// SearchFanOut decorator which exposes the user-spec
	// Option{Hits, Latencies} Stats surface. Composition-stable
	// failure mode: a BuildCanonicalSearchFanOut error aborts
	// boot so a misconfigured backend set is visible at startup
	// rather than silently degrading to partial coverage.
	//
	// Note: when root.Search.ProviderRegistry is nil (registry
	// not built — e.g. test fixtures or partial deploys), the
	// fan-out is set to a noopFanOut that surfaces
	// ErrAggregatorNil on every Search and returns the empty
	// map on Stats. Handlers map this to 503 (services not
	// wired) without panicking on a nil decorator.
	var searchFanOut search.SearchFanOut
	var searchBackends *search.BackendRegistry
	var searchAgg *providers.SearchAggregator
	if root.Search != nil && root.Search.ProviderRegistry != nil {
		searchFanOut, searchBackends, _ = BuildCanonicalSearchFanOut(SearchBackendBuildOpts{
			Logger:      log,
			ProviderReg: root.Search.ProviderRegistry,
			ClipsRepo:   root.Repos.ClipsRepo,
		})
		searchAgg = providers.NewSearchAggregator(root.Search.ProviderRegistry)
		log.Info("PR-2: canonical SearchFanOut wired against root.Search.ProviderRegistry (single shared instance)")
	}
	if yw, err := WireYouTubeClip(cfg, log, root.Domains.YoutubeClipService, root.Jobs.Facade, root.Jobs.Service, root.Repos.ClipsRepo, toolCheckerAdapter, idemHandler, searchAgg); err != nil {
		log.Warn("failed to wire module", zap.String("module", "YouTubeClip"), zap.Error(err))
	} else {
		if err := tryRegisterModuleStrict(registry, log, yw.Module, WithRegistrationPoint("register.YouTubeClip")); err != nil {
			return nil, fmt.Errorf("wire registry: youtube: %w", err)
		}
		wiring.YouTubeClip = yw
	}

	// ── Per-capability inventory (PR 1, June 2026): UNROLLED from the
	// previous `for _, m := range []struct{...}` loop so:
	//   1. Each capability has exactly one typed block at the composition
	//      site (no anonymous function-tuple shape).
	//   2. The 4 side-builds (realtime / generation / channels /
	//      search_queries) that were trapped INSIDE the previous loop body
	//      now execute EXACTLY ONCE — they had no dependence on the loop
	//      iteration variable. The hoisted block is the next section
	//      below the inventory.
	// Each block follows the same shape: build → tryRegisterModuleStrict
	// → propagate wiring handle. The strict path is mandatory after the
	// PR 1 deletion of the coalescing tryRegisterModule safe-skip helper
	// (silent duplicates now surface as hard composition errors).
	var imagesHandler *imagesapi.ImagesHandler

	// 1) Jobs — thin wrapper, no bundle deps.
	{
		// PR-0 (June 2026): NewJobsHandler signature is now
		// (job.Service, JobStatsReader, *zap.Logger). *root.Jobs.Service
		// satisfies both interfaces — it implements the canonical domain
		// job.Service (orchestrator) AND the JobStatsReader port (via the
		// runtime type-assertion GetStats helper). When a future stats source
		// is bindable without the orchestrator's mutation surface, pass that
		// reader to h.stats and let h.service continue carrying the
		// orchestrator.
		jobsHandler := jobsapi.NewJobsHandler(root.Jobs.Service, root.Jobs.Service, log)
		jobsMod := module.NewRouteModule(
			"jobs",
			func() bool { return true },
			"/jobs",
			jobsHandler,
			log,
		)
		log.Info("created Jobs module")
		if err := tryRegisterModuleStrict(registry, log, jobsMod, WithRegistrationPoint("register.Jobs")); err != nil {
			return nil, fmt.Errorf("wire registry: jobs: %w", err)
		}
	}

	// 2) Images — needs MediaIngest wiring for upstream service injection.
	{
		var ingestSvc *ingest.Service
		if wiring.MediaIngest != nil {
			ingestSvc = wiring.MediaIngest.Service
		}
		imagesHandler = imagesapi.NewImagesHandler(root.Domains.ImageService, ingestSvc)
		imagesMod := module.NewRouteModule(
			"images",
			func() bool { return cfg.Features.ImagesEnabled },
			"/images",
			imagesHandler,
			log,
		)
		log.Info("created Images module")
		if err := tryRegisterModuleStrict(registry, log, imagesMod, WithRegistrationPoint("register.Images")); err != nil {
			return nil, fmt.Errorf("wire registry: images: %w", err)
		}
	}

	// 3) MediaIngest — bundle-driven; reuses root.<bundle> paths. PR8:
	// idemHandler installed on POST /api/media/ingest.
	{
		ingestBundle := &MediaIngestBundle{
			DB:                root.DB,
			Assets:            root.Repos.Assets,
			DriveClient:       root.Drive.DriveClient,
			ImageRepo:         root.Repos.ImageRepo,
			VoiceoverRepo:     root.Repos.VoiceoverRepo,
			ClipsRepo:         root.Repos.ClipsRepo,
			AssetIndexService: root.Search.AssetIndexService,
			PrebuiltService:   root.Domains.IngestService,
			// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed):
			// roots the canonical outbox.Dispatcher into
			// WireMediaIngest so the 4 NewClipStoreAdapter +
			// NewClipsRegistry ctor calls in module_media.go get a
			// non-nil mutations SSOT (production canonical path).
			Dispatcher:    root.Outbox.Dispatcher,
		}
		mediaIngestW, mediaIngestErr := WireMediaIngest(cfg, log, ingestBundle, idemHandler)
		wiring.MediaIngest = mediaIngestW
		if mediaIngestErr != nil {
			log.Warn("failed to wire module", zap.String("module", "MediaIngest"), zap.Error(mediaIngestErr))
		} else if mediaIngestW != nil && mediaIngestW.Module != nil {
			if err := tryRegisterModuleStrict(registry, log, mediaIngestW.Module, WithRegistrationPoint("register.MediaIngest")); err != nil {
				return nil, fmt.Errorf("wire registry: media-ingest: %w", err)
			}
		}
	}

	// 4) Scraper — thin wrapper, infra-only deps.
	{
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
			return nil, fmt.Errorf("wire registry: scraper: %w", err)
		}
	}

	// 5) FullImages — bundle-driven; uses ImageService + MediaStore.
	{
		fullImagesW, fullImagesErr := WireFullImages(cfg, log, root.Domains.ImageService, root.Drive.MediaStore)
		wiring.FullImages = fullImagesW
		if fullImagesErr != nil {
			log.Warn("failed to wire module", zap.String("module", "FullImages"), zap.Error(fullImagesErr))
		} else if fullImagesW != nil && fullImagesW.Module != nil {
			if err := tryRegisterModuleStrict(registry, log, fullImagesW.Module, WithRegistrationPoint("register.FullImages")); err != nil {
				return nil, fmt.Errorf("wire registry: full-images: %w", err)
			}
		}
	}

	// 6) StockPipeline — bundle-driven.
	{
		stockBundle := &StockBundle{
			DriveClient:        root.Drive.DriveClient,
			Jobs:               root.Jobs.Service,
			JobFacade:          root.Jobs.Facade,
			AssetIndexService:  root.Search.AssetIndexService,
			ClipsRepo:          root.Repos.ClipsRepo,
			YoutubeClipService: root.Domains.YoutubeClipService,
			ClipIndexerService: root.Process.ClipIndexerService,
			Dispatcher:         root.Outbox.Dispatcher,
		}
		stockW, stockErr := WireStockPipeline(cfg, log, stockBundle)
		wiring.StockPipeline = stockW
		if stockErr != nil {
			log.Warn("failed to wire module", zap.String("module", "StockPipeline"), zap.Error(stockErr))
		} else if stockW != nil && stockW.Module != nil {
			if err := tryRegisterModuleStrict(registry, log, stockW.Module, WithRegistrationPoint("register.StockPipeline")); err != nil {
				return nil, fmt.Errorf("wire registry: stock-pipeline: %w", err)
			}
		}
	}

	// ─────────────────────────────────────────────────────────────────
	// HOISTED SIDE-BUILDS (PR 1, June 2026) — execute ONCE, not 6×.
	// The 4 capability clusters below were trapped inside the previous
	// `for` body, which meant they executed per iteration. They have
	// NO dependence on the loop iteration variable and now run exactly
	// once, in a deterministic order. Each call site uses the strict
	// helper (tryRegisterModuleStrict).
	// ─────────────────────────────────────────────────────────────────

	// realtime (clip-search lateral capability; Wave 14 close). Kept
	// inline because (a) it is a single guarded NewRouteModule call and
	// (b) the codex/registry-loop-fix branch scope extracts only the
	// three capabilities the spec names (registerGenerationCapability,
	// registerChannelsCapability, registerSearchQueriesCapability).
	// Wave 15 (June 2026): DomainBundle.RealtimeMatcher is the typed
	// assetsapi.RealtimeMatcher — drop the runtime cast.
	if root.Domains != nil && root.Domains.RealtimeMatcher != nil {
		realtimeEnabled := false // Realtime package removed (commit d61068b3)
		matcher := root.Domains.RealtimeMatcher
		if err := tryRegisterModuleStrict(registry, log, module.NewRouteModule(
			"realtime",
			func() bool { return root.Domains.RealtimeMatcher != nil && realtimeEnabled },
			"",
			assetsapi.NewRealtimeMatchHandler(matcher, log),
			log,
		), WithRegistrationPoint("register.Realtime")); err != nil {
			return nil, fmt.Errorf("wire registry: realtime module: %w", err)
		}
	}

	// ── Hoisted capability extractions (PR 1, June 2026 — branch
	// codex/registry-loop-fix). The 3 named functions below replace
	// inline blocks that the previous `for _, m := range []struct{...}`
	// loop body executed N× per WireRegistry call. PR 1 closes that
	// loop and moves each block here as a small named function. Each
	// function:
	//   - calls the canonical Build factory exactly once,
	//   - registers the resulting Descriptor via tryRegisterModuleStrict
	//     exactly once,
	//   - publishes any DescriptorJobs/DescriptorProviders slots WITHOUT
	//     re-registering the module (clear Register vs PublishSlots
	//     separation — see codex/registry-strict-uniqueness branch for
	//     the dedicated enforcement).
	// Build/registration/freeze-order invariants are pinned by
	// internal/app/registry_loop_test.go (this branch's Definition of Done).
	if err := registerGenerationCapability(registry, log, cfg, root); err != nil {
		return nil, fmt.Errorf("wire registry: generation: %w", err)
	}
	if err := registerChannelsCapability(registry, log, root); err != nil {
		return nil, fmt.Errorf("wire registry: channels: %w", err)
	}
	if err := registerSearchQueriesCapability(registry, log, root); err != nil {
		return nil, fmt.Errorf("wire registry: search_queries: %w", err)
	}

	if wiring.MediaIngest != nil {
		log.Info("MediaIngest module wired (service pre-built via BuildDomainBundle, no late-binding needed)")
	}
	if root.Repos != nil && root.Repos.ScriptsRepo != nil {
		// NewScriptHistoryModule expects two gin.HandlerFunc gate args
		// (handler feature gate + enabled bool). The helper in
		// internal/api/middleware reads the resolved boolean and wraps
		// it in a 403-on-disabled middleware. Script history is shared by
		// all script entrypoints, so we keep it alive whenever any script
		// feature is enabled.
		scriptHistoryEnabled := anyScriptFeatureEnabled(cfg)
		if err := tryRegisterModuleStrict(registry, log, scriptapi.NewScriptHistoryModule(
			scriptapi.NewScriptHistoryHandler(scriptcore.NewRepositoryAdapter(root.Repos.ScriptsRepo), log),
			log,
			middleware.FeatureFlagChecker("Script", scriptHistoryEnabled),
			scriptHistoryEnabled,
		), WithRegistrationPoint("register.ScriptHistory")); err != nil {
			return nil, fmt.Errorf("wire registry: script-history module: %w", err)
		}
	}
	if err := tryRegisterModuleStrict(registry, log, module.NewUtilityModule(cfg, log, root.Utility.Utility), WithRegistrationPoint("register.Utility")); err != nil {
		return nil, fmt.Errorf("wire registry: utility module: %w", err)
	}

	// PR4d-chunk2: maintenanceSvc constructed locally (no longer assigned to CoreDeps);
	// voiceoverSvc selected from root.Domains; assets bundle built from root.
	maintenanceSvc := maintenance.NewService(cfg, log, root.Search.AssetIndexService, root.Search.AssetTreeService, root.Maint.DeletionSvc, root.Jobs.Service, root.DB.DB)
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		log.Warn("failed to register maintenance handler", zap.Error(err))
	}

	var voiceoverService *voiceover.Service
	if root.Domains.VoiceoverService != nil {
		voiceoverService = root.Domains.VoiceoverService
	}

	assetsBundle := &AssetsBundle{
		ClipsRepo:               root.Repos.ClipsRepo,
		VoiceoverRepo:           root.Repos.VoiceoverRepo,
		ImageRepo:               root.Repos.ImageRepo,
		Assets:                  root.Repos.Assets,
		DriveClient:             root.Drive.DriveClient,
		AssetTreeService:        root.Search.AssetTreeService,
		AssetIndexService:       root.Search.AssetIndexService,
		MediaProcessor:          root.Process.MediaProcessor,
		CatalogSyncService:      root.Sync.CatalogSync,
		ClipIndexerService:      root.Process.ClipIndexerService,
		IdempotencyStore:        root.Repos.IdempotencyStore,
		IdempotencyStoreHandler: idemHandler,
		// PR-2 (June 2026): the canonical SearchFanOut is stamped
		// onto the bundle BEFORE WireAssets runs. WireAssets
		// consumes the pre-built slot rather than constructing
		// its own (single shared instance invariant — see
		// AssetsBundle package doc).
		SearchFanOut:          searchFanOut,
		SearchBackendRegistry: searchBackends,
	}
	// Wave 16 (June 2026): WireAssets realtimeSvc is typed
	// `assetsapi.RealtimeMatcher` (no more `interface{}` carrier).
	// Pass-through is direct: DomainBundle.RealtimeMatcher → WireAssets
	// (typed-to-typed, no auto-bridge required).
	if aw, err := WireAssets(cfg, log, assetsBundle, root.Jobs, voiceoverService, root.Domains.VoiceoverSync, root.Domains.RealtimeMatcher, root.Repos.CatalogRepo, maintenanceSvc, root.Search.ProviderRegistry, root.Outbox.Dispatcher); err == nil && aw != nil {
		wiring.Assets = aw
		if err := tryRegisterModuleStrict(registry, log, aw.Module, WithRegistrationPoint("register.Assets")); err != nil {
			return nil, fmt.Errorf("wire registry: assets module: %w", err)
		}
		if maintenanceSvc != nil && aw.DeletionSvc != nil {
			maintenanceSvc.SetDeletionService(aw.DeletionSvc)
			log.Info("injected DeletionService into MaintenanceService")
		}
	}

	// ── QDRANT-002: build canonical internal outbox handler ─────────
	// Exposes GET /internal/v1/outbox/status and /events for operator
	// dashboard visibility into the outbox events pipeline (pending,
	// processing, dead_letter, completed, superseded counts + event list).
	//
	// QDRANT-002 (June 2026) separation-of-routes fix: the handler is
	// constructed here but NOT registered in the public /api registry —
	// that caused /api/internal/v1/outbox/* to leak past the WorkerAuth
	// boundary. The handler is now passed to AppDeps.OutboxHandler and
	// mounted on the /internal/v1 WorkerAuth-protected internalGroup by
	// cmd/server/main.go. See internal/api/routes.go::Setup for the
	// wiring site; the test internal/api/routes_test.go::TestRoutes_
	// NoApiInternalV1Prefix enforces this split at CI time.
	if root.Outbox != nil && root.Outbox.EventsRepo != nil {
		// Wave 14 PR5 (June 2026): wrap the concrete *outboxevents.Repository
		// in a typed outbox.MonitorPort adapter so the api layer stays free of
		// internal/infrastructure/* imports. Adapter is constructed here
		// because the api package must not import outboxevents directly per
		// AGENTS.md Pattern 8 ("API package: thin transport only").
		outboxPort := newOutboxMonitorAdapter(root.Outbox.EventsRepo)
		outboxH := outboxapi.NewHandler(outboxPort, log)
		wiring.OutboxHandler = outboxH
		log.Info("QDRANT-002: outbox events handler BUILT (mounted on /internal/v1/outbox via AppDeps, NOT via /api)")
	}

	// ── QDRANT-004: build mediasearch handler ─────────────────────────
	// Wires the unified media search API at POST /internal/v1/media/search
	// when Qdrant is enabled and the vector store adapter is available.
	//
	// QDRANT-004 (June 2026) separation-of-routes fix: same reasoning as
	// the outbox handler above — the handler is constructed here but NOT
	// registered through the public /api registry (which would mount it
	// at /api/internal/v1/media/* outside the WorkerAuth boundary).
	// AppDeps.MediasearchHandler is mounted on internalGroup by
	// cmd/server/main.go.
	if root.Process.VectorSvc != nil && root.AI != nil && root.AI.OllamaClient != nil {
		// Wave 15 (June 2026): ProcessBundle.VectorSvc is the typed
		// assetsearch.VectorStorePort — no runtime cast needed.
		// portutil.IsNilPort is the typed-nil safety net (catches the
		// `(*searchAdapter)(nil)` case if a future refactor accidentally
		// injects a typed-nil concrete; the field type guard above is
		// the front line).
		vectorStore := root.Process.VectorSvc
		if vectorStore != nil && !portutil.IsNilPort(vectorStore) {
			// Build the VectorSearchPort adapter: OllamaClient for embedding +
			// Qdrant search adapter for vector store operations.
			// Wave 15 (June 2026): ProcessBundle.VectorSvc is the typed
			// assetsearch.VectorStorePort — `vectorStore` above is direct read.
			// Compile-time assertion at internal/infrastructure/qdrant/search_adapter.go
			// guarantees the qdrant adapter satisfies the port.
			// PR 10 (June 2026): the canonical *search.Aggregator is the
			// SOLE wire for media search results. The handler now takes
			// WireParams{Aggregator, Log}. wiring.Assets.SearchAggregator
			// is populated by WireAssets' BuildSearchBackends + NewAggregator
			// (search_backends.go). When nil, the handler returns 503 on
			// every Search call (fail-closed) per godlike/07.
			var searchAgg mediasearchapi.AggregatorSearcher
			if wiring.Assets != nil && wiring.Assets.SearchAggregator != nil {
				searchAgg = wiring.Assets.SearchAggregator
			}
			searchH := mediasearchapi.NewHandler(mediasearchapi.WireParams{Aggregator: searchAgg, Log: log})
			wiring.MediasearchHandler = searchH
			log.Info("QDRANT-004: mediasearch handler BUILT (mounted on /internal/v1/media/search via AppDeps, NOT via /api)")
		}
	}

	// ── ProviderRegistry — register adapters + FREEZE at the end ─────
	// Lives on SearchBundle (PR4 review): it's an asset-search dispatch
	// registry, not a Drive-sync concern.
	if root.Search != nil && root.Search.ProviderRegistry != nil {
		pr := root.Search.ProviderRegistry
		if wiring.ArtlistSvc != nil && wiring.ArtlistSvc.Service != nil {
			if err := pr.RegisterSearch(artlistadapter.NewAdapter(wiring.ArtlistSvc.Service)); err != nil {
				log.Warn("failed to register artlist provider", zap.Error(err))
			} else {
				log.Info("registered artlist provider in providers.Registry")
			}
		} else {
			log.Info("artlist service unavailable — skipping provider registration")
		}
		if wiring.YouTubeClip != nil && wiring.YouTubeClip.Service != nil {
			if err := pr.RegisterSearch(youtubeadapter.NewAdapter(wiring.YouTubeClip.Service)); err != nil {
				log.Warn("failed to register youtube provider", zap.Error(err))
			} else {
				log.Info("registered youtube provider in providers.Registry")
			}
		} else {
			log.Info("youtube clip service unavailable — skipping provider registration")
		}
		if wiring.StockPipeline != nil && wiring.StockPipeline.Service != nil {
			if err := pr.RegisterFetch(stockadapter.NewAdapter(wiring.StockPipeline.Service)); err != nil {
				log.Warn("failed to register stock fetch provider", zap.Error(err))
			} else {
				log.Info("registered stock fetch provider in providers.Registry")
			}
		} else {
			log.Info("stock pipeline service unavailable — skipping fetch provider registration")
		}
		// ── ScriptAssets capability (Capability Standard DescriptorProviders
		// slot migration, June 2026): the script_assets capability is wired
		// via scriptassets.Build(deps). Build returns a single Descriptor that
		// carries:
		//   - the api.Module for /script-assets routes, AND
		//   - the api.DescriptorProviders slot which the composition root uses
		//     to publish the script_assets catalog entry (provider identity +
		//     capabilities) into the canonical providers.Registry.
		//
		// This is the "richer" capability migration demonstrating the slot
		// pattern's RANGE beyond DescriptorJobs: DescriptorProviders is a
		// one-shot composition-time publication of catalog identity (not per-
		// job runtime registration like DescriptorJobs). Both slots coexist
		// on the same Descriptor mechanism; the composition root type-asserts
		// for each independently.
		//
		// RegisterProviders must run BEFORE pr.Freeze() below; the registry
		// must be mutable when the descriptor publishes into it. Frozen
		// registries return ErrFrozen from Register, so ordering matters.
		scDesc, scErr := scriptassets.Build(scriptassets.Dependencies{
			Logger: log,
		})
		if scErr != nil {
			log.Warn("failed to wire module", zap.String("module", "script-assets"), zap.Error(scErr))
		} else {
			if err := tryRegisterModuleStrict(registry, log, scDesc, WithRegistrationPoint("register.ScriptAssets")); err != nil {
				return nil, fmt.Errorf("wire registry: script-assets: %w", err)
			}
			// *ScriptAssetsDescriptor satisfies api.Descriptor via the three
			// explicit delegation methods (Name/Enabled/RegisterRoutes), and
			// api.DescriptorProviders via RegisterProviders. Same concrete
			// pointer cast as the generation block above.
			if dp, ok := scDesc.(*scriptassets.ScriptAssetsDescriptor); ok {
				if err := dp.RegisterProviders(pr); err != nil {
					return nil, fmt.Errorf("wire registry: script-assets providers: %w", err)
				}
				log.Info("registered script_assets catalog entry in providers.Registry",
					zap.String("name", "script_assets"),
					zap.Strings("capabilities", []string{"search", "script"}))
			}
		}
		// FREEZE here, after all registrations. (Reviewer Q8 fix.)
		pr.Freeze()
		log.Info("providers.Registry frozen at end of WireRegistry",
			zap.Int("providers", len(pr.All())))

		if wiring.Assets != nil && wiring.Assets.Module != nil {
			log.Info("providers.Registry wired into Assets module via constructor")
		}
		if wiring.YouTubeClip != nil && wiring.YouTubeClip.Handler != nil {
			log.Info("YouTubeClipHandler wired transport-only (PR-CLIP-YT-REGISTRY-CLEANUP: providers.Registry dispatch published via youtubeadapter.NewAdapter above; handler no longer takes providerRegistry)")
		}
	}

	return wiring, nil
}

// ── loop-kill named functions (PR 1, June 2026 — branch
// codex/registry-loop-fix). Each function is called exactly once per
// WireRegistry invocation from the hoisted block directly above; the
// previous loop shape trapped these blocks in a `for` body where they
// ran once per loop iteration. PR 1 closes the loop and extracts each
// block as a small named function so the Build + Register + slot
// publication counts are trivially auditable from the call sites.

// registerGenerationCapability wires the Capability-Standard unified
// generation endpoint at /api/generations via generation.Build(deps).
// Build runs at most once per call. The returned Descriptor is
// registered via tryRegisterModuleStrict exactly once. The
// api.DescriptorJobs slot publishes books.process + lessons.process
// worker handlers to Jobs.Service WITHOUT re-registering the module —
// the slot-publication path is a separate Register/PublishSlots path
// per the codex/registry-strict-uniqueness branch.
//
// Returning nil on Build failure keeps the registry mutable so
// WireRegistry's later phases are not poisoned. The strict path will
// surface a hard error on any subsequent register-generation attempt
// (pin per TestRegisterGenerationCapability_RepeatedCallsFailFast).
func registerGenerationCapability(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot) error {
	if root.Domains == nil {
		return nil
	}
	var booksHandler generation.HandlerFunc
	if root.Domains.BooksService != nil {
		booksHandler = root.Domains.BooksService.HandleJob
	}
	var lessonsHandler generation.HandlerFunc
	if root.Domains.LessonsService != nil {
		lessonsHandler = root.Domains.LessonsService.HandleJob
	}
	genDesc, err := generation.Build(generation.Dependencies{
		Jobs:           root.Jobs.Service,
		Assets:         root.Repos.Assets,
		Books:          booksHandler,
		Lessons:        lessonsHandler,
		BooksEnabled:   cfg.Books.Enabled,
		LessonsEnabled: cfg.Lessons.Enabled,
		ScriptEnabled:  anyScriptFeatureEnabled(cfg),
		Logger:         log,
	})
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "generation"), zap.Error(err))
		return nil
	}
	if err := tryRegisterModuleStrict(registry, log, genDesc, WithRegistrationPoint("register.Generation")); err != nil {
		return fmt.Errorf("wire registry: generation: %w", err)
	}
	// PublishSlots (api.DescriptorJobs). RegisterJobHandlers is the
	// slot-publication method; it does NOT re-register the module.
	// The strict-path Register call above already placed the Descriptor
	// in the module registry; this call only publishes worker handlers.
	if dj, ok := genDesc.(module.DescriptorJobs); ok {
		if err := dj.RegisterJobHandlers(root.Jobs.Service); err != nil {
			log.Warn("failed to register generation job handlers", zap.Error(err))
		}
	}
	return nil
}

// registerChannelsCapability wires the channels capability via
// channels.Build(deps). Build runs at most once per call; the resulting
// Descriptor is registered via tryRegisterModuleStrict exactly once.
func registerChannelsCapability(registry *module.Registry, log *zap.Logger, root *ComposeRoot) error {
	if root.DB == nil || root.DB.DB == nil {
		return nil
	}
	d, err := channels.Build(channels.Dependencies{
		Repository: channels.NewRepositoryAdapter(assets.NewChannelsRepository(root.DB.DB)),
		Logger:     log,
	})
	if err != nil {
		log.Warn("failed to wire module", zap.String("module", "channels"), zap.Error(err))
		return nil
	}
	if err := tryRegisterModuleStrict(registry, log, d, WithRegistrationPoint("register.Channels")); err != nil {
		return fmt.Errorf("wire registry: channels: %w", err)
	}
	return nil
}

// registerSearchQueriesCapability wires the typed search_queries use case.
// The handler is thin transport; this function owns the
// *searchqueriesuc.UseCase construction (Wave 14 problem #3 close-out,
// June 2026) and registers the route module via tryRegisterModuleStrict.
func registerSearchQueriesCapability(registry *module.Registry, log *zap.Logger, root *ComposeRoot) error {
	if root.DB == nil || root.DB.DB == nil {
		return nil
	}
	if err := tryRegisterModuleStrict(registry, log, module.NewRouteModule(
		"search_queries",
		func() bool { return true },
		"/search-queries",
		assetsapi.NewSearchQueriesHandler(searchqueriesuc.NewUseCase(assets.NewSearchQueriesRepository(root.DB.DB)), log),
		log,
	), WithRegistrationPoint("register.SearchQueries")); err != nil {
		return fmt.Errorf("wire registry: search_queries module: %w", err)
	}
	return nil
}

// wireScriptFlow is defined in wire_script.go.
