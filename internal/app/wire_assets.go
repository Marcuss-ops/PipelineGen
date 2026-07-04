// Package app — Assets module wiring (PR-WIRE-ASSETS-CAPABILITY-SPLIT, July 2026).
//
// WireAssets is a thin orchestrator that delegates each capability to a
// dedicated `build*Bundle` function in a sibling file. The 5 capability
// bundles (clips / storage / diagnostics / search / voiceover) are
// extracted to single-purpose files; soundeffect + register stay inline
// here per the user's literal 5-file request.
//
// Per AGENTS.md Pattern 5 (per-capability decomposition), the linear
// pipeline shape guarantees the failure-mode ordering matches the call
// order (clip → storage → diagnostics → search → voiceover → soundeffect
// → register → assetsMod assembly), so any fail-closed error returns
// early before later bundles are constructed.
//
// Cross-references:
//   - wire_assets_clips.go       — buildClipsBundle (largest capability)
//   - wire_assets_storage.go     — buildStorageBundle
//   - wire_assets_diagnostics.go — buildDiagnosticsBundle
//   - wire_assets_search.go      — buildSearchBundle
//   - wire_assets_voiceover.go   — buildVoiceoverBundle
//
// The voiceoverSvc + voiceoverSync + realtimeSvc + maintenanceSvc
// params are retained in the WireAssets signature for the typed-port
// chain (godlike/07 framework) even though the body no longer
// references them — see PR4d-chunk2 (June 2026) for the historical
// rationale. In Go, unused function parameters are permitted by the
// language spec, so no `_ =` discard is needed.
//
// PR-WIRE-ASSETS-CAPABILITY-SPLIT (2026-07-04, deadline 2026-08-15): the
// YAGNI `catalogRepo "may reuse"` param from the pre-split signature
// (was at position 8) is removed; the caller in registry_assets.go is
// updated to drop the `root.Repos.CatalogRepo` argument. The
// appsearch.Service consumer that "may reuse" catalogRepo was deleted
// in Wave 21 PR 10 (June 2026) and the catalogRepo param survived as
// dead code; the per-capability split is the right place to retire it.
package app

import (
	"fmt"
	"os"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	assetregister "github.com/Marcuss-ops/PipelineGen/internal/api/assets/register"
	assetsfx "github.com/Marcuss-ops/PipelineGen/internal/api/assets/soundeffect"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	voiceoverreconcile "github.com/Marcuss-ops/PipelineGen/internal/application/assets/reconciliation/voiceover"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	voiceoverpkg "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// WireAssets creates the unified Assets handler and module.
//
// PR-WIRE-ASSETS-CAPABILITY-SPLIT (2026-07-04, deadline 2026-08-15):
//   - Body is a linear pipeline of 7 build*Bundle calls (5 file-extracted
//   - 2 inline soundeffect + register). Each capability build is
//     fail-closed (returns the typed-NIL-safe descriptor or an error).
//   - The YAGNI `catalogRepo "may reuse"` param (pre-split position 8)
//     is REMOVED from the signature; registry_assets.go caller updated
//     to drop the `root.Repos.CatalogRepo` argument.
//
// PR-WIRE-ASSETS-NIL-CLASSIFICATION (2026-07-25, dep_classification.go):
//   - Every descriptor type-assertion goes through ClassifyDepGet
//     (DepRequired in all 7 sites — production fail-closed).
//
// PR4d-chunk2 (June 2026): takes *AssetsModuleDeps + 6 narrow direct args
// (catalogRepo removed by this PR; 4 legacy params retained for
// godlike/07 typed-port framework).
func WireAssets(
	cfg *config.Config,
	log *zap.Logger,
	deps *AssetsModuleDeps,
	jobs *JobsBundle,
	voiceoverSvc *voiceoverpkg.Service, // legacy, retained per godlike/07 framework
	voiceoverSync *voiceoverreconcile.Service, // legacy, retained per godlike/07 framework
	realtimeSvc assetsapi.RealtimeMatcher, // legacy, retained per godlike/07 framework
	maintenanceSvc *maintenance.Service, // legacy, retained per godlike/07 framework
	providerRegistry *providers.Registry,
	dispatcher *outbox.Dispatcher,
) (*AssetsWiring, error) {
	// (1) Extract common infrastructure
	var driveUploader *driveutil.Uploader
	if deps.Delivery.Admin != nil {
		if up, ok := deps.Delivery.Admin.(*driveutil.Uploader); ok {
			driveUploader = up
		}
	}
	var assetRepo asset.Repository
	if deps.Core.Assets != nil {
		assetRepo = deps.Core.Assets.Repository()
	}

	searchBackends := deps.Search.SearchBackendRegistry
	searchFanOut := deps.Search.SearchFanOut
	if err := ClassifyDepGet("WireAssets: deps.Search.SearchFanOut is nil (composition root must call BuildCanonicalSearchFanOut before WireAssets)", searchFanOut == nil, DepRequired, log); err != nil {
		return nil, err
	}
	searchAggregator := search.NewAggregator(searchBackends, &zapSearchLogAdapter{log: log})
	log.Info("WireAssets: consumed pre-built canonical SearchFanOut",
		zap.Int("backends", len(searchBackends.All())))

	folderMemSvc := foldermemory.NewService(log, deps.Core.ClipsRepo)
	metaWriter := semantic.NewMetadataWriter(cfg.Paths.PythonScriptsDir, cfg.Storage.TempPath(), cfg.External.OllamaURL, cfg.External.OllamaModel, log)
	deletionSvc := deletion.NewDeletionService(deps.Core.ClipsRepo, deps.Core.ClipsRepo, deps.Core.ClipsRepo, deps.Core.VoiceoverRepo, deps.Core.ImageRepo, driveUploader, deps.Core.AssetTreeService, deps.Core.AssetIndexService, dispatcher, nil, nil, log)

	var idemHandler gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if deps.Background.IdempotencyStore != nil {
		idemHandler = deps.Background.IdempotencyStoreHandler
	}

	// (2) Build capability bundles (linear pipeline; order matches the
	// canonical *Descriptor flow used by assetsapi.NewModule below)
	clipsDesc, err := buildClipsBundle(cfg, log, deps, jobs, dispatcher, driveUploader, assetRepo, searchAggregator, metaWriter, folderMemSvc, deletionSvc, idemHandler)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: clips: %w", err)
	}

	storageDesc, err := buildStorageBundle(log, jobs, deps.Core.CatalogSyncService)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: storage: %w", err)
	}

	dd, diagSvc, err := buildDiagnosticsBundle(log, deps.Core.ClipsRepo)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: diagnostics: %w", err)
	}

	sd, err := buildSearchBundle(log, searchAggregator)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: search: %w", err)
	}
	if diagSvc != nil {
		log.Info("diagnostics and search services wired with production ports")
	} else {
		log.Warn("diagnostics service NOT fully wired — some routes will return 503")
	}

	vd, err := buildVoiceoverBundle(log, jobs)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: voiceover: %w", err)
	}

	soundeffectDesc, err := buildSoundeffectBundle(cfg, log, deps, metaWriter, driveUploader, dispatcher)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: soundeffect: %w", err)
	}

	rd, err := buildRegisterBundle(cfg, log, deps, driveUploader, providerRegistry, clipsDesc, idemHandler, dispatcher, jobs)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: register: %w", err)
	}

	// (3) Final assembly — assetsapi.NewModule consumes the 7 descriptors
	assetsMod := assetsapi.NewModule(assetsapi.Dependencies{
		Storage:     storageDesc,
		Diagnostics: dd,
		Search:      sd,
		Clips:       clipsDesc,
		Voiceover:   vd,
		SoundEffect: soundeffectDesc,
		Register:    rd,
	}, log)
	assetsRouteMod := module.NewRouteModule(
		"assets",
		func() bool { return true },
		"/media",
		assetsMod,
		log,
	)
	log.Info("created unified Assets module (thin transport)")

	return &AssetsWiring{
		Module:               assetsRouteMod,
		DeletionSvc:          deletionSvc,
		InternalMediaHandler: storageDesc.Handler,
		SearchAggregator:     searchAggregator,
		SearchFanOut:         searchFanOut,
	}, nil
}

// buildSoundeffectBundle is the inline soundeffect capability builder.
//
// The user-requested 5-file split (PR-WIRE-ASSETS-CAPABILITY-SPLIT) does
// not include soundeffect; this helper stays in wire_assets.go for
// consistency with the linear-pipeline shape. SoundEffect: wrapped
// repos + uploader + metaWriter + dispatcher via sfxports adapters.
// PG-003 (June 2026) replaced the four concrete infrastructure
// reach-throughs with structural ports so the api/ layer stays thin
// (per AGENTS.md Pattern 0).
func buildSoundeffectBundle(
	cfg *config.Config,
	log *zap.Logger,
	deps *AssetsModuleDeps,
	metaWriter *semantic.MetadataWriter,
	driveUploader *driveutil.Uploader,
	dispatcher *outbox.Dispatcher,
) (*assetsfx.SoundeffectDescriptor, error) {
	sfxClips := &sfxClipsRepoAdapter{repo: deps.Core.ClipsRepo}
	sfxMeta := &sfxSemanticWriterAdapter{w: metaWriter}
	sfxResolver := &sfxResolverAdapter{r: driveutil.NewResolver("data", "")}

	sfxDispatcher := newSfxDispatcherAdapter(dispatcher)
	descriptor, err := assetsfx.Build(assetsfx.Dependencies{
		ClipsRepo:              sfxClips,
		MetaWriter:             sfxMeta,
		Resolver:               sfxResolver,
		Dispatcher:             sfxDispatcher,
		Publisher:              deps.Delivery.Publisher,
		SoundEffectsRootFolder: cfg.Drive.SoundEffectsRootFolder,
		ProcessRunner:          processRunnerAdapter,
		EnabledFunc:            func() bool { return true },
		ModuleOpts:             nil,
		Logger:                 log,
	})
	if err != nil {
		return nil, err
	}
	desc, ok := descriptor.(*assetsfx.SoundeffectDescriptor)
	if err := ClassifyDepGet(fmt.Sprintf("WireAssets: soundeffect (got %T, want *assetsfx.SoundeffectDescriptor)", descriptor), !ok || desc == nil, DepRequired, log); err != nil {
		return nil, err
	}
	return desc, nil
}

// buildRegisterBundle is the inline register capability builder.
//
// The user-requested 5-file split (PR-WIRE-ASSETS-CAPABILITY-SPLIT) does
// not include register; this helper stays in wire_assets.go for
// consistency with the linear-pipeline shape. The sourcingEnrichmentAdapter
// (constructed inside newAssetRegisterService) is the one non-HTTP
// consumer of the clips orchestrator — it calls
// handler.EnrichAndIndexClip(ctx, clip, source) — which the
// ClipsDescriptor exposes via its Handler field.
//
// PR-DRIVE-AVAILABILITY-GATE (2026-07-04): builds a driveChecker
// closure that mirrors the boot-time validateDriveServiceAvailability
// check — returns nil iff driveUploader is wired AND cfg stat-OK.
// Threaded through assetregister.Dependencies.DriveChecker so the
// handler-level preflight at BatchRegisterFromYouTube fail-closed 503
// when folder_id is non-empty AND *drive.Uploader.Service is nil
// (the canonical silent-failure mode before this PR).
func buildRegisterBundle(
	cfg *config.Config,
	log *zap.Logger,
	deps *AssetsModuleDeps,
	driveUploader *driveutil.Uploader,
	providerRegistry *providers.Registry,
	clipsDesc *clipsapi.ClipsDescriptor,
	idemHandler gin.HandlerFunc,
	dispatcher *outbox.Dispatcher,
	jobs *JobsBundle,
) (*assetregister.RegisterDescriptor, error) {
	registerSvc := newAssetRegisterService(cfg, log, deps.Core.ClipsRepo, driveUploader, deps.Core.AssetTreeService, providerRegistry, clipsDesc.Handler, dispatcher, deps.Delivery.Publisher, jobs.Service)

	// PR-DRIVE-AVAILABILITY-GATE: compose the canonical driveChecker
	// closure. Two-state probe (godlike/06 SSOT one-canonical-owner-per-fact):
	//   (a) runtime — driveUploader != nil means BuildDriveBundle wired
	//       Drive successfully at boot AND validateDriveServiceAvailability
	//       passed; the *drive.Uploader.Service field is non-nil reachable
	//       from sourcing.Service.BatchRegisterFromYouTube routing.
	//   (b) config — cfg.Paths.CredentialsFile + cfg.Paths.TokenFile
	//       exist on disk (mirrors validateDriveServiceAvailability;
	//       belt-and-suspenders for the operator-toggled
	//       StrictStartupValidation=false soft-mode escape hatch).
	// Either state failing returns a typed error that BatchRegisterFromYouTube
	// surfaces as HTTP 503 with the actionable diagnostic — pre-PR, the
	// same request 500-panicked on first clip dispatch.
	driveChecker := func() error {
		if driveUploader == nil {
			return fmt.Errorf("drive uploader not wired at composition time (build_bundles_drive.go::BuildDriveBundle returned driveClient==nil; PR-DRIVE-AVAILABILITY-GATE fail-closed forbids folder_id traffic)")
		}
		if driveUploader.Service == nil {
			return fmt.Errorf("driveUploader.Service is nil despite driveUploader non-nil (typed-NIL interface trap; PR-DRIVE-AVAILABILITY-GATE fail-closed forbids folder_id traffic)")
		}
		if cfg == nil {
			return fmt.Errorf("cfg is nil at driveChecker invocation (composition root invariant violated; PR-DRIVE-AVAILABILITY-GATE fail-closed)")
		}
		if _, err := os.Stat(cfg.GetCredentialsPath()); err != nil {
			return fmt.Errorf("Drive credentials missing at driveChecker invocation: %w (PR-DRIVE-AVAILABILITY-GATE fail-closed; rerun python3 scripts/generate_drive_token.py to fix)", err)
		}
		if _, err := os.Stat(cfg.GetTokenPath()); err != nil {
			return fmt.Errorf("Drive token missing at driveChecker invocation: %w (PR-DRIVE-AVAILABILITY-GATE fail-closed; rerun python3 scripts/generate_drive_token.py to fix)", err)
		}
		return nil
	}

	descriptor, err := assetregister.Build(assetregister.Dependencies{
		Service:      registerSvc,
		Idempotency:  idemHandler,
		EnabledFunc:  func() bool { return true },
		ModuleOpts:   nil,
		Logger:       log,
		DriveChecker: driveChecker,
	})
	if err != nil {
		return nil, err
	}
	desc, ok := descriptor.(*assetregister.RegisterDescriptor)
	if err := ClassifyDepGet(fmt.Sprintf("WireAssets: register (got %T, want *assetregister.RegisterDescriptor)", descriptor), !ok || desc == nil, DepRequired, log); err != nil {
		return nil, err
	}
	return desc, nil
}
