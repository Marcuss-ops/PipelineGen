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

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
		publisher = drive.NewPublisher(registry, folderMgr, driveUploader, log)
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

	return &DriveBundle{
		DriveClient:   driveClient,
		DriveUploader: driveUploader,
		DocClient:     docClient,
		DriveDests:    dests,
		MediaStore:    mediaStore,
		DestResolver:  destResolver,
		StyleRegistry: styleRegistry,
		Publisher:     publisher,
	}, startClosure, nil
}
