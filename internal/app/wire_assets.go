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
// PR-WIRE-ASSETS-CAPABILITY-SPLIT (2026-07-04, deadline 2026-08-15):
//   - Body is a linear pipeline of 7 build*Bundle calls (5 file-extracted +
//     2 inline soundeffect + register). Each capability build is
//     fail-closed (returns the typed-NIL-safe descriptor or an error).
//   - The YAGNI `catalogRepo "may reuse"` param from the pre-split
//     signature (was at position 8) is REMOVED; the caller in
//     registry_assets.go is updated to drop the `root.Repos.CatalogRepo`
//     argument. The appsearch.Service consumer that "may reuse" catalogRepo
//     was deleted in Wave 21 PR 10 (June 2026) and the catalogRepo param
//     survived as dead code until this split.
//   - The four legacy typed-port params (voiceoverSvc, voiceoverSync,
//     realtimeSvc, maintenanceSvc) were also removed; they had become
//     dead weight after their consumers were retired.
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
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
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
// // PR4d-chunk2 (June 2026): takes *AssetsModuleDeps + 2 narrow direct args
// (catalogRepo and 4 legacy params removed by this PR).
func WireAssets(
	cfg *config.Config,
	log *zap.Logger,
	deps *AssetsModuleDeps,
	jobs *JobsBundle,
	lifecycle driveutil.FileLifecycle,
	providerRegistry *providers.Registry,
	dispatcher *outbox.Dispatcher,
) (*AssetsWiring, error) {
	// (1) Extract common infrastructure
	var driveUploader *driveutil.Uploader
	if deps.Delivery.Admin != nil {
		if up, ok := deps.Delivery.Admin.(*driveutil.Uploader); ok {
			// PR-ADAPTER-NIL-GUARD (2026-07-06): typed-NIL interface trap.
			// `deps.Delivery.Admin.(*driveutil.Uploader)` returning ok=true
			// is not sufficient — the interface can hold a typed nil pointer.
			// Subsequent dereference of any nil *Uploader field panics at
			// the first port call (e.g., deletionSvc → drive check). Guard
			// fail-closed at composition time so the deployment crashes
			// loudly at boot, not silently mid-flight.
			if up == nil {
				return nil, fmt.Errorf("WireAssets: drive.Admin: direct *driveutil.Uploader is nil (typed-NIL interface trap; PR-ADAPTER-NIL-GUARD fail-closed)")
			}
			driveUploader = up
		} else if adapter, ok := deps.Delivery.Admin.(*driveutil.AdminAdapter); ok {
			// DRIVE-005 (FASE 9): Admin is now *AdminAdapter (embedding *Uploader) since
			// PR-DRIVECLIENT-RAW-RETIRE (2026-07-04). The type-assertion above
			// (*Uploader) fails because the concrete type is now *AdminAdapter;
			// we unwrap the embedded *Uploader so the downstream driveUploader
			// consumers (deletionSvc, buildClipsBundle, buildRegisterBundle)
			// receive the canonical concrete uploader they expect.
			//
			// PR-ADAPTER-NIL-GUARD (2026-07-06): fail-closed with typed
			// sentinel — see drive.ErrAdminAdapterUploaderNil. Canonical
			// construction via NewAdminAdapter rejects nil u (admin.go:64)
			// so this branch only succeeds on test paths or post-hoc
			// mutation; either way, dereferencing adapter.Uploader would
			// nil-panic on the first port call.
			if adapter.Uploader == nil {
				return nil, driveutil.WrapDriveAdminError(driveutil.ErrAdminAdapterUploaderNil)
			}
			driveUploader = adapter.Uploader
		} else {
			// PR-ADAPTER-NIL-GUARD (2026-07-06) — godlike/07 NO-FAKE-AVAILABILITY:
			// Branch 3 silent-nil fallthrough. If deps.Delivery.Admin is
			// NEITHER *Uploader NOR *AdminAdapter (a future port variant, a
			// regression that misroutes Admin to the wrong concrete type, a
			// test stub that bypasses both branches), fail-closed at the
			// composition root with a typed sentinel rather than leave
			// driveUploader nil silently — the failure would otherwise
			// surface later as a panic mid-flight on the first port call.
			return nil, driveutil.WrapDriveAdminError(driveutil.ErrAdminUnknownType)
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
	// P0-#2 (July 2026): the sfx capability is the canonical
	// graceful-degradation consumer (the handler logs the error and
	// continues). Wire the explicit nop so sfxSemanticWriterAdapter
	// receives a non-nil port and the handler's h.metaWriter != nil
	// check fires the Write call (which returns the typed sentinel
	// and gets logged). Other consumers (clips, voiceover) get nil
	// from their respective composition-root sites.
	metaWriter := semantic.NewNopMetadataWriter(log)
	deletionSvc := deletion.NewDeletionService(deps.Core.ClipsRepo, deps.Core.ClipsRepo, deps.Core.ClipsRepo, deps.Core.VoiceoverRepo, deps.Core.ImageRepo, driveUploader, deps.Core.AssetTreeService, deps.Core.AssetIndexService, dispatcher, nil, nil, log)

	var idemHandler gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if deps.Background.IdempotencyStore != nil {
		idemHandler = deps.Background.IdempotencyStoreHandler
	}

	// (2) Build capability bundles (linear pipeline; order matches the
	// canonical *Descriptor flow used by assetsapi.NewModule below)
	clipsDesc, clipEnricher, err := buildClipsBundle(buildClipsParams{
		Cfg:              cfg,
		Log:              log,
		Deps:             deps,
		Jobs:             jobs,
		Dispatcher:       dispatcher,
		DriveUploader:    driveUploader,
		AssetRepo:        assetRepo,
		SearchAggregator: searchAggregator,
		MetaWriter:       metaWriter,
		FolderMemSvc:     folderMemSvc,
		DeletionSvc:      deletionSvc,
		IdemHandler:      idemHandler,
	})
	if err != nil {
		return nil, fmt.Errorf("WireAssets: clips: %w", err)
	}

	// PR-STEP7-DESCRIPTOR-JOBS (July 2026): publish clips worker handlers
	// (bulk_upload_youtube_clips) into the canonical jobs service at boot.
	// Pre-Step-7, ClipsDescriptor did NOT implement DescriptorJobs so this
	// slot was never invoked — the bulk_upload_youtube_clips handler was
	// never registered and the worker could never claim those jobs.
	// clipsDesc is *ClipsDescriptor (concrete); wrap in any for
	// the type assertion (mirrors registerGenerationCapability pattern
	// where genDesc is already api.Descriptor).
	//
	// PR-DIAG-BULKUPLOAD-REGISTRATION (July 2026, diagnostic-only):
	// snapshot the jobs-service pointer at the composition site so a
	// future "no handler registered" reproduction can compare the
	// descriptor-side svc_ptr (logged inside ClipsDescriptor.RegisterJobHandlers)
	// against the transport-side bt_jobsSvc_ptr (logged inside
	// BulkUploadYouTubeClips on request entry). Pointer equality across
	// both ends would rule out the "two different *jobs.Service instances"
	// hypothesis; a mismatch would localize the split. Diagnostic only —
	// no behavioural change; the lines should be retired in a follow-up
	// commit once the upstream bug is fixes.
	log.Info("WireAssets: pre-DescriptorJobs-registration jobs-service snapshot",
		zap.String("jobs_service_type", fmt.Sprintf("%T", jobs.Service)),
		zap.String("jobs_service_ptr", fmt.Sprintf("%p", jobs.Service)),
		zap.String("jobs_facade_type", fmt.Sprintf("%T", jobs.Facade)),
		zap.String("jobs_facade_ptr", fmt.Sprintf("%p", jobs.Facade)),
		zap.String("clipsDesc_type", fmt.Sprintf("%T", clipsDesc)),
		zap.String("clipsDesc_ptr", fmt.Sprintf("%p", clipsDesc)),
	)
	if dj, ok := any(clipsDesc).(module.DescriptorJobs); ok {
		log.Info("WireAssets: clipsDesc type-asserted as DescriptorJobs; calling RegisterJobHandlers")
		if err := dj.RegisterJobHandlers(jobs.Service); err != nil {
			log.Warn("failed to register clips job handlers", zap.Error(err))
		} else {
			log.Info("WireAssets: clips job handlers registered (descriptor-side ok)")
		}
	} else {
		log.Warn("WireAssets: clipsDesc does NOT implement module.DescriptorJobs — bulk_upload_youtube_clips will be unhandled (DIAG: this is bug-localisation signal A)",
			zap.String("clipsDesc_type", fmt.Sprintf("%T", clipsDesc)),
			zap.String("clipsDesc_ptr", fmt.Sprintf("%p", clipsDesc)),
		)
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

	rd, err := buildRegisterBundle(cfg, log, deps, lifecycle, driveUploader, providerRegistry, clipEnricher, idemHandler, dispatcher, jobs)
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
	metaWriter semantic.MetadataWriterPort,
	driveUploader *driveutil.Uploader,
	dispatcher *outbox.Dispatcher,
) (*assetsfx.SoundeffectDescriptor, error) {
	sfxClips := &sfxClipsRepoAdapter{repo: deps.Core.ClipsRepo}
	sfxMeta := &sfxSemanticWriterAdapter{w: metaWriter}
	sfxResolver := &sfxResolverAdapter{mediaRoot: "data"}

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
// consistency with the linear-pipeline shape.
//
// Card 10 (July 2026): the sourcingEnrichmentAdapter (constructed
// inside newAssetRegisterService) is the canonical non-HTTP consumer
// of the clips enrich path — it now consumes the appclips.ClipEnricher
// typed port (godlike/06 SSOT: boundary type lives at the application
// layer; the descriptor exposes only routes + job handlers and no
// longer exposes any *clips.Handler field).
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
	lifecycle driveutil.FileLifecycle,
	driveUploader *driveutil.Uploader,
	providerRegistry *providers.Registry,
	clipEnricher appclips.ClipEnricher,
	idemHandler gin.HandlerFunc,
	dispatcher *outbox.Dispatcher,
	jobs *JobsBundle,
) (*assetregister.RegisterDescriptor, error) {
	registerSvc := newAssetRegisterService(cfg, log, deps.Core.ClipsRepo, driveUploader, lifecycle, deps.Core.AssetTreeService, providerRegistry, clipEnricher, dispatcher, deps.Delivery.Publisher, jobs.Service)

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
