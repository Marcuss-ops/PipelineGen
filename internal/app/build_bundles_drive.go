// Package app — Drive bundle construction (FASE 2.B PR1, June 2026).
//
// Originally lived in build_bundles_process.go alongside BuildProcessBundle
// + BuildOutboxBundle. PR1 extracts the Drive construction (this file)
// and the Drive runtime startup validation (build_drive_startup.go) into
// dedicated files so each file owns a single bundle concept per AGENTS.md
// Pattern 5. PR1 is MOVE-only: zero logic changes, zero call-site changes.
// BuildDriveBundle's signature and return shape are preserved (composition.go
// calls it from NewComposition with no adjustments).
//
// Cross-references:
//   - internal/app/build_drive_startup.go: houses startDriveBackgroundFolders
//     (Drive folder pre-creation, AC validation, local storage dirs).
//     Captured as the wiring.IOpaqueStartFunc closure returned from BuildDriveBundle.
//   - internal/app/build_bundles_process.go: builds BuildProcessBundle +
//     BuildOutboxBundle (Qdrant-derivable media + canonical outbox).
//   - internal/app/composition.go: defines *wiring.DriveBundle struct + calls
//     BuildDriveBundle from NewComposition.
package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"os"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	sqlitedelivery "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// configOnlyDestinations builds *wiring.DriveDestinations from config only (no runtime resolution).
// Moved from composition.go (PR-GODOBJ-7 composition target, July 2026).
func configOnlyDestinations(cfg *config.Config) *wiring.DriveDestinations {
	return &wiring.DriveDestinations{MediaRoot: cfg.Drive.RootFolder(), SoundEffectsRoot: cfg.Drive.SoundEffectsRootFolder, ImagesFolderID: cfg.Drive.ImagesFolder()}
}

// BuildDriveBundle constructs the Drive adapters + MediaStore + DestResolver.
// Loads StyleRegistry at the top so the canonical StyleRegistry is available
// for downstream Style-aware consumers (CompositionRoot.processor path),
// but no longer drives a side-effecting pre-creation goroutine — that
// path was REMOVED in Wave A Item 15 (June 2026).
//
// PR9-A (June 2026): BuildDriveBundle returns an wiring.IOpaqueStartFunc closure
// that defers side-effecting initialisation (Drive folder validation,
// storage directory creation) to the lifecycle. The bundle itself is
// fully populated on return.
//
// Wave A Item 15 (June 2026): the concurrent.SafeGo("drive-style-folders", ...)
// call site that called ensureStyleDriveFolders is REMOVED. The values
// the closure used to populate (per-style Drive folder IDs) are now
// derived lazily at request time by the canonical drive.Admin
// GetOrCreateFolder path via the destinations resolver (see
// delivery.NewDestinationRegistry).
//
// FASE 2.B PR1 (June 2026): extracted to this file from
// build_bundles_process.go. Surface preserved (no signature change).
// Call site at composition.go::NewComposition is unchanged.
//
// PR-DRIVE-AVAILABILITY-GATE (2026-07-04): the TOF now invokes
// validateDriveServiceAvailability(cfg) which fail-closes at boot
// when strict-mode is on AND credentials.json / token.json are
// missing from disk. The gate precedes ALL existing wire-up
// statements so the godlike/06 SSOT "one canonical owner per fact"
// invariant holds: this composition site delegates the
// Drive-availability decision to the sole canonical helper. Soft-mode
// (cfg.Drive.StrictStartupValidation=false) leaves startup validation soft; the
// handler-level preflight at BatchRegisterFromYouTube still
// fail-closed 503 at request time per godlike/07 defense-in-depth.
func BuildDriveBundle(ctx context.Context, cfg *config.Config, dbs *wiring.Databases, log *zap.Logger) (*wiring.DriveBundle, wiring.IOpaqueStartFunc, error) {
	// PR-DRIVE-AVAILABILITY-GATE: boot-time fail-closed gate. Surfaces a
	// typed error with an actionable fix hint when the operator has
	// strict-mode on but credentials.json + token.json are missing
	// from disk (the canonical silent-failure mode that previously
	// caused *drive.Uploader.Service to be nil and POST register-batch
	// with folder_id non-empty to 500-panic). Soft-mode operators
	// (StrictStartupValidation=false) leaves this validation soft; the
	// handler-level preflight at internal/api/assets/register/handler.go::BatchRegisterFromYouTube
	// still fail-closes 503 at request time.
	if err := validateDriveServiceAvailability(cfg); err != nil {
		return nil, nil, err
	}

	styleRegistry, _ := generation.NewStyleRegistry("config/generation_styles.yaml")

	docClient, err := drive.NewDocClient(ctx, cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		log.Warn("Docs client not initialized", zap.Error(err))
	}

	// P1-6 (July 2026): derive the canonical delivery.DocPublisher port
	// from the concrete DocClient. Go cannot implicitly convert between
	// two interface types with identical method-sets, so we type-assert.
	// The assertion is safe because NewDocClient always returns
	// *DocClientImpl, and the compile-time assertion at
	// internal/infrastructure/drive/doc_publisher_assert.go locks the
	// conformance at build time. nil-safe: if docClient is nil (Drive
	// not configured), DocPublisher stays nil.
	var docPublisher delivery.DocPublisher
	if docClient != nil {
		var ok bool
		docPublisher, ok = docClient.(delivery.DocPublisher)
		if !ok {
			return nil, nil, fmt.Errorf("compose drive: DocClient does not satisfy delivery.DocPublisher (P1-6 migration incomplete)")
		}
	}

	driveClient, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		log.Warn("Google Drive client not initialized", zap.Error(err))
	}

	// PG-011-residual-cleanup (June 2026): the previous
	// resolveRuntimeDestinations function (a no-op alias for
	// configOnlyDestinations — both pre-existing branches converged
	// on the same cfg-derived *wiring.DriveDestinations) was deleted;
	// dests is now derived once, unconditionally. driveClient
	// remains a dependency for driveUploader construction, the
	// mediaStore block below, and the startClosure's folder
	// validation, but it is no longer threaded through a
	// dests-resolution alias that ignored it.
	var driveUploader *drive.Uploader
	var dests = configOnlyDestinations(cfg)
	if driveClient != nil {
		driveUploader = &drive.Uploader{Service: driveClient, Log: log}
	}

	// FASE 3 (June 2026): construct the canonical Publisher.
	// The Publisher is the single canal for all Drive writes;
	// endpoints use it instead of calling driveUploader/folderManager directly.
	var publisher delivery.Publisher
	if driveClient != nil && driveUploader != nil {
		registry := delivery.NewDestinationRegistry(cfg)
		folderMgr := drive.NewDriveFolderManagerAdapter(driveClient, log)
		// P0 #3 fail-fast (June 2026): NewPublisher now returns
		// (*Publisher, error) with ErrMissingXxx sentinels. The
		// composition root treats a nil-port misconfiguration as a
		// hard process-startup failure (gated as a checked error
		// rather than a runtime panic) so operators see a typed
		// sentinel + audit message instead of a deferred nil-deref
		// at the first Publish call site. Note: the surrounding
		// `if driveClient != nil && driveUploader != nil` guard
		// already covers the typed-NIL interface trap (godlike/06);
		// the explicit error check below is a defence-in-depth
		// against future call-site regressions that bypass the guard.
		var pub *drive.Publisher
		pub, err = drive.NewPublisher(registry, folderMgr, driveUploader, log)
		if err != nil {
			log.Error("drive.Publisher: composition-time fail-fast barrier hit (Pattern 0 port is nil — typed-NIL interface trap averted)",
				zap.Bool("registry_nil", errors.Is(err, drive.ErrMissingDestinationRegistry)),
				zap.Bool("folders_nil", errors.Is(err, drive.ErrMissingFolderManager)),
				zap.Bool("files_nil", errors.Is(err, drive.ErrMissingFileUploader)),
				zap.Error(err))
			return nil, nil, err
		}
		publisher = pub

		// DoD item 6 (SEMANTIC-LOCATION-API-2026-07-06): wire the local
		// drive_folder_catalog into the Publisher so it consults cached
		// folder IDs before making Drive API calls. Nil-tolerant: when
		// the catalog table doesn't exist yet (fresh DB), the adapter
		// returns nil and SetCatalogLookup becomes a no-op.
		catalogRepo := sqlitedelivery.NewRepository(dbs.DualPool.Writer)
		catalog := drive.NewCatalogFolderLookup(catalogRepo)
		pub.SetCatalogLookup(catalog)
		if writer, ok := catalog.(drive.CatalogFolderWriter); ok {
			pub.SetCatalogWriter(writer)
		}
	}

	// DEV-STUB (July 2026): when Drive is not configured, inject a stub
	// publisher so the server can start for smoke-testing without Google
	// credentials. The stub surfaces clear errors on any actual
	// Publish/ResolveFolder call. Production deployments MUST configure
	// Drive credentials (this log line is the operator's reminder).
	if publisher == nil {
		log.Warn("DEV-STUB: Google Drive not configured — injecting stub publisher (Publish/ResolveFolder will return typed errors). Production MUST set Drive credentials.")
		publisher = &driveStubPublisher{log: log}
	}

	// PR9-A (June 2026): side-effecting initialisation is delegated to
	// startDriveBackgroundFolders (defined in build_drive_startup.go).
	// Package-level function cross-file call so the source-level
	// goroutine-count freeze test reports zero spawns in BuildDriveBundle's
	// own body.
	// Lifecycle-runtime-ownership (June 2026): now returns error so
	// serverLifecycle.Start can abort on required folder validation failure.
	startClosure := func() error {
		return startDriveBackgroundFolders(ctx, cfg, driveClient, driveUploader, dests, log)
	}

	// FASE 9 (June 2026, P0.1 / DRIVE-005): wire the canonical Pattern 0
	// ports (Admin / Reader) alongside the deprecated DriveClient /
	// DriveUploader fields. *drive.Uploader satisfies both ports
	// structurally — the compile-time asserts at the bottom of
	// internal/infrastructure/drive/ports.go pin conformance.
	//
	// Typed-nil-safety: assigning a nil *drive.Uploader directly to a
	// drive.Admin field produces a non-nil interface holding a nil
	// pointer (Go interface nilness trap). The probe is precisely the
	// site that needs to distinguish "not configured" (Drive disabled)
	// from "configured but unreachable" (auth failure), so we must hand
	// back a TRUE nil interface when the uploader is nil. The explicit
	// `var admin drive.Admin` + `if driveUploader != nil { admin = ... }`
	// pattern is required (an inline ternary or direct assignment won't
	// keep the interface value true-nil).
	var admin drive.Admin
	var reader drive.Reader
	var lifecycle drive.FileLifecycle
	if driveUploader != nil {
		// P0.4 admin scope (June 2026): Admin port goes through
		// *AdminAdapter (folderLookupFunc seam applied to GetOrCreateFolder).
		// Production callers reach GetOrCreateFolder via drive.EnsureFolderPath
		// from stock/stockpipeline/util.go:42 and application/assets/ingest/drive.go:40.
		// Reader + Lifecycle retain the *Uploader backing (same seam shape
		// doesn't apply to them). drive.NewAdminAdapter is typed-nil-safe
		// (returns nil only if driveUploader is nil — and the `if driveUploader != nil`
		// guard above already screens that path).
		admin = drive.NewAdminAdapter(driveUploader, log)
		reader = driveUploader
		// CARD-3 (June 2026): Lifecycle is the canonical Pattern 0 port for
		// file-lifecycle Drive ops (Trash/Move/Rename/Cleanup). Reuses
		// driveUploader.Service so Drive credentials are loaded exactly
		// once at composition time (godlike/06 split from Admin).
		lifecycle = drive.NewFileLifecycleAdapter(driveUploader.Service, log)
	}

	return &wiring.DriveBundle{
		// Canonical Pattern 0 ports (FASE 9 P0.1 / DRIVE-005).
		Admin:  admin,
		Reader: reader,
		// PR-DRIVECLIENT-RAW-RETIRE (2026-07-04): the previous
		// `DriveClient: driveClient,` field assignment is REMOVED. The
		// local `driveClient *gdrive.Service` is retained as a function-
		// local variable for the driveUploader ctor + the startClosure's
		// startDriveBackgroundFolders call (which still takes a raw
		// *gdrive.Service for the start-time FolderManager probe — that
		// function is INTERNAL to the composition root and does NOT
		// surface the raw SDK via the bundle). The 4 Pattern 0 ports
		// above (Admin / Reader / DocClient / Lifecycle) are the ONLY
		// canonical Drive surface on the bundle per godlike/06 SSOT
		// (one owner per fact). Deprecation record:
		// architecture/deprecations.yaml#DRIVE-RAW-BUNDLE-LEAK.
		DriveUploader: driveUploader, // unexported; for internal wiring within package app
		DriveDests:    dests,
		// PR-IMAGES-REMOVE-DRIVE-STORE (July 2026): wiring.DriveBundle.MediaStore
		// field was REMOVED. DestResolver field stays on the struct
		// (nil-tolerant for legacy non-image callers like voiceover/artlist
		// that still consume asset.Resolver through the bundle) but is
		// no longer wired here — the previous drive.NewDestinationResolver
		// impl was tied to drive.Store, which has been retired.
		DestResolver:  nil,
		StyleRegistry: styleRegistry,
		Publisher:     publisher,
		DocClient:     docClient,
		DocPublisher:  docPublisher,
		Lifecycle:     lifecycle,
	}, startClosure, nil
}

var _ delivery.Publisher = (*driveStubPublisher)(nil)

// driveStubPublisher is a no-op delivery.Publisher used when Google Drive
// is not configured (dev/smoke-test environments). Publish and ResolveFolder
// both return typed errors so callers that actually need Drive surface the
// gap loudly, while the server can still boot and serve read-only endpoints.
type driveStubPublisher struct {
	log *zap.Logger
}

func (s *driveStubPublisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	s.log.Warn("driveStubPublisher.Publish: Drive not configured",
		zap.String("destination", string(req.Destination)),
		zap.String("filename", req.Filename))
	return nil, fmt.Errorf("drive not configured: cannot publish %q to %q (DEV-STUB)", req.Filename, req.Destination)
}

func (s *driveStubPublisher) ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error) {
	s.log.Warn("driveStubPublisher.ResolveFolder: Drive not configured",
		zap.String("destination", string(req.Destination)))
	return "", fmt.Errorf("drive not configured: cannot resolve folder for %q (DEV-STUB)", req.Destination)
}

// startDriveBackgroundFolders (moved from build_drive_startup.go, Phase 5 consolidation, June 2026).
// Performs side-effecting Drive init: validates critical Drive folder
// paths, and ensures local storage directories exist.
//
// Wave A Item 15 (June 2026): the legacy `concurrent.SafeGo("drive-style-folders", ...)`
// spawn that called ensureStyleDriveFolders is REMOVED. Per-style Drive
// folder pre-creation is no longer a composition-time concern; it is
// the operator's responsibility via `reset-video-ai` (canonical CLI).
// The `styleRegistry` parameter has been removed from this signature —
// BuildDriveBundle still constructs the StyleRegistry (other consumers
// in the bundle tree may read it), but the start-closure no longer
// needs a handle to it.
//
// P1.3 (July 2026): the legacy images-only Drive folder pre-validation
// (the `for name, folderID := range map[string]string{"images": dests.ImagesFolder()}`
// block) is replaced by the registry-driven delivery.StartupDriveRootsValidator.
// Every DestinationRegistry policy's RootFolderID is now probed via
// FolderManagerPort.ProbeFolderAccess (read-only, retry-with-jitter);
// the composition-time stub now honours the strict-mode flag
// (cfg.Drive.StrictStartupValidation) instead of hard-coding fail-fast
// on the single images folder. Operators leaving
// StrictStartupValidation at the default (true) get the legacy
// "fail-at-boot" behaviour generalised across all 9 destinations;
// soft-mode (false) logs the per-destination failures and proceeds
// so the legacy "discover at first upload" surface remains available
// for staging / DR runs.
func startDriveBackgroundFolders(
	ctx context.Context,
	cfg *config.Config,
	driveClient *gdrive.Service,
	driveUploader *drive.Uploader,
	dests *wiring.DriveDestinations,
	log *zap.Logger,
) error {
	// P1.3: registry-driven startup root validation. Replaces the
	// pre-P1.3 images-only check with a uniform probe across every
	// DestinationRegistry policy's RootFolderID. The validator
	// itself is constructed regardless of `driveClient != nil`
	// (the typed-NIL-safe construct handles nil service by
	// returning ErrMissingFolderManager — but NO real validator is
	// needed when Drive auth failed at composition time, because
	// Builder already logged the DriveClient-nil case and downstream
	// surfaces will fail loudly at first publish).
	if driveClient != nil && driveUploader != nil {
		registry := delivery.NewDestinationRegistry(cfg)
		folderMgr := drive.NewDriveFolderManagerAdapter(driveClient, log)
		// P1.4 (July 2026): wire the canonical metrics surface so the
		// SRE dashboard sees per-destination probe counters + latency
		// histograms + run-summary gauges. The struct is built against
		// the promauto package globals declared in
		// internal/infrastructure/observability/metrics_delivery.go
		// — production wiring always uses this constructor so all four
		// metrics auto-register with the default Prometheus registry.
		metrics := delivery.NewDriveValidatorMetrics()
		validator, vErr := delivery.NewDriveRootsValidator(registry, folderMgr, log, metrics)
		if vErr != nil {
			// Should be unreachable (registry is constructed, folderMgr
			// is constructed). Log + halt so a future drift surfaces
			// loudly at composition time, not at first publish.
			log.Error("startDriveBackgroundFolders: validator construction failed (P1.3 invariant: registry+folderMgr MUST be non-nil)",
				zap.Bool("registry_nil", errors.Is(vErr, delivery.ErrMissingStartupValidatorRegistry)),
				zap.Bool("folders_nil", errors.Is(vErr, delivery.ErrMissingStartupValidatorFolders)),
				zap.Error(vErr))
			if cfg.Drive.StrictStartupValidation {
				return fmt.Errorf("startDriveBackgroundFolders: validator construction failed: %w", vErr)
			}
		} else {
			report, valErr := validator.ValidateDriveRoots(ctx)
			if valErr != nil {
				log.Error("startDriveBackgroundFolders: Drive root validation FAILED",
					zap.Int("failed_count", len(report.FailedDestinations())),
					zap.Strings("failed_destinations", stringifyDestinations(report.FailedDestinations())),
					zap.Error(valErr),
				)
				if cfg.Drive.StrictStartupValidation {
					return fmt.Errorf("startDriveBackgroundFolders: %w", valErr)
				}
				// Soft mode: log the failures but proceed. The
				// affected destinations will fail-fast at first Publish
				// call (the legacy "discover at first upload" surface).
			} else {
				log.Info("startDriveBackgroundFolders: all configured Drive roots validated",
					zap.Int("validated_count", len(report.PerDestination)),
					zap.Int("skipped_count", len(report.Skipped)),
				)
			}
		}
	}
	for _, dir := range []string{
		cfg.Storage.DataDir, cfg.Storage.VoiceoversPath(), cfg.Storage.AssetsPath(),
		cfg.Storage.DownloadsPath(), cfg.Storage.BackupsPath(), cfg.Storage.TempPath(),
		cfg.Storage.AnimationsPath(), cfg.Storage.YoutubeClipsPath(),
		cfg.Storage.ArtlistPath(), cfg.Storage.ImagesPath(),
	} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Warn("Failed to create storage directory", zap.String("path", dir), zap.Error(err))
		}
	}
	return nil
}

// stringifyDestinations formats []delivery.DestinationKey as a
// comma-separated string for log readability. Kept local to the
// composition root so the type's API surface is not polluted with
// presentation helpers. Called only inside the P1.3 log branch +
// the soft-mode conditional, so cost is bounded to per-failure log
// lines (rare).
func stringifyDestinations(destinations []delivery.DestinationKey) []string {
	out := make([]string, 0, len(destinations))
	for _, d := range destinations {
		out = append(out, string(d))
	}
	return out
}
