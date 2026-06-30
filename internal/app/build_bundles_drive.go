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
//     Captured as the IOpaqueStartFunc closure returned from BuildDriveBundle.
//   - internal/app/build_bundles_process.go: builds BuildProcessBundle +
//     BuildOutboxBundle (Qdrant-derivable media + canonical outbox).
//   - internal/app/composition.go: defines *DriveBundle struct + calls
//     BuildDriveBundle from NewComposition.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// BuildDriveBundle constructs the Drive adapters + MediaStore + DestResolver.
// Loads StyleRegistry at the top so ensureStyleDriveFolders (called via the
// returned startDriveBackgroundFolders closure in build_drive_startup.go)
// receives the non-nil pointer.
//
// PR9-A (June 2026): BuildDriveBundle returns an IOpaqueStartFunc closure
// that defers side-effecting initialisation (Drive folder validation,
// style-folder pre-creation, storage directory creation) to the lifecycle.
// The bundle itself is fully populated on return.
//
// FASE 2.B PR1 (June 2026): extracted to this file from
// build_bundles_process.go. Surface preserved (no signature change).
// Call site at composition.go::NewComposition is unchanged.
func BuildDriveBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, search *SearchBundle) (*DriveBundle, IOpaqueStartFunc, error) {
	styleRegistry, _ := generation.NewStyleRegistry("config/generation_styles.yaml")

	docClient, err := drive.NewDocClient(ctx, cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		log.Warn("Docs client not initialized", zap.Error(err))
	}

	driveClient, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		log.Warn("Google Drive client not initialized", zap.Error(err))
	}

	// PG-011-residual-cleanup (June 2026): the previous
	// resolveRuntimeDestinations function (a no-op alias for
	// configOnlyDestinations — both pre-existing branches converged
	// on the same cfg-derived *DriveDestinations) was deleted;
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
	}

	var mediaStore *drive.Store
	var destResolver asset.Resolver
	if driveClient != nil {
		storageResolver := drive.NewResolver(
			drive.MediaRoot(cfg.Storage.MediaPath()),
			drive.DriveRoot(dests.RootFolder()),
		)

		// Construct the StoreOptions at the ctor boundary — no post-ctor
		// SetAssetTree / SetTreeSource calls. TreeSources maps Drive folder
		// IDs to their logical tree source names.
		storeOpts := drive.StoreOptions{}
		if search != nil && search.AssetTreeService != nil {
			storeOpts.AssetTree = search.AssetTreeService
			storeOpts.TreeSources = map[string]string{
				dests.ImagesFolder(): "image",
			}
			log.Info("mediaStore: Drive roots configured",
				zap.String("images_folder_id", dests.ImagesFolder()))
		}

		mediaStore = drive.NewStoreWithOptions(
			storageResolver,
			driveUploader,
			dests.RootFolder(),
			dests.ImagesFolder(),
			"", // VideoAIRoot removed (PR June 2026) — pass empty string
			dests.SoundEffectsRoot,
			log,
			storeOpts,
		)

		destResolver = drive.NewDestinationResolver(mediaStore)
	}

	// PR9-A (June 2026): side-effecting initialisation is delegated to
	// startDriveBackgroundFolders (defined in build_drive_startup.go).
	// Package-level function cross-file call so the source-level
	// goroutine-count freeze test reports zero spawns in BuildDriveBundle's
	// own body.
	// Lifecycle-runtime-ownership (June 2026): now returns error so
	// serverLifecycle.Start can abort on required folder validation failure.
	startClosure := func() error {
		return startDriveBackgroundFolders(ctx, cfg, driveClient, driveUploader, dests, styleRegistry, log)
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

	return &DriveBundle{
		// Canonical Pattern 0 ports (FASE 9 P0.1 / DRIVE-005).
		Admin: admin,
		Reader: reader,
		// Wave B (June 2026): DriveUploader field removed from DriveBundle.
		// Wave C (June 2026 — partial): DriveUploader + DriveClient
		// deprecated fields removed from DriveBundle. cmd/admin/ callers
		// reach Drive via root.Drive.Admin / root.Drive.Reader — no
		// further raw SDK reach-through from cmd/admin/.
		//
		// Wave D Commit 1 (June 2026 — mechanical migration): the
		// residual 13 sites (artlist package diagnostic field rename,
		// internal/app/ docs refresh) now consume the DriveFolderManager
		// port; DriveClient REMAINS on DriveBundle as the back-compat
		// plumbing channel that feeds the ArtlistBundle.DriveClient
		// threading path (registry_internal_modules.go::registerArtlist)
		// and the *drive.DriveFolderManagerAdapter construction in
		// WireArtlist. Future Wave D Commits may retire the field +
		// drop the gdrive import pending operator signal on whether
		// the back-compat affordance is still load-bearing.
		DriveClient:    driveClient,
		driveUploader:  driveUploader, // unexported; for internal wiring within package app
		DriveDests:     dests,
		MediaStore:     mediaStore,
		DestResolver:   destResolver,
		StyleRegistry:  styleRegistry,
		Publisher:      publisher,
		DocClient:      docClient,
		Lifecycle:      lifecycle,
	}, startClosure, nil
}

// startDriveBackgroundFolders (moved from build_drive_startup.go, Phase 5 consolidation, June 2026).
// Performs side-effecting Drive init: pre-creates style folders, validates
// critical Drive folder paths, and ensures local storage directories exist.
func startDriveBackgroundFolders(
	ctx context.Context,
	cfg *config.Config,
	driveClient *gdrive.Service,
	driveUploader *drive.Uploader,
	dests *DriveDestinations,
	styleRegistry *generation.StyleRegistry,
	log *zap.Logger,
) error {
	if driveClient != nil && dests.ImagesFolder() != "" && dests.ImagesFolder() != dests.MediaRoot {
		concurrent.SafeGo("drive-style-folders", func() {
			ensureStyleDriveFolders(ctx, driveUploader, dests.ImagesFolder(), styleRegistry, log)
		})
		log.Info("Style Drive folders using Images root", zap.String("folder_id", dests.ImagesFolder()))
	}
	if driveClient != nil {
		for name, folderID := range map[string]string{
			"images": dests.ImagesFolder(),
		} {
			if folderID == "" {
				continue
			}
			if _, err := driveClient.Files.Get(folderID).Fields("id, name").Context(ctx).Do(); err != nil {
				return fmt.Errorf("required Drive folder %q (id=%s) validation failed: %w", name, folderID, err)
			}
			log.Info("Drive folder validated",
				zap.String("folder_name", name), zap.String("folder_id", folderID))
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
