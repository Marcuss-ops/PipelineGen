// Package app — Drive bundle construction (split out from composition.go
// in commit ci/composition-split wave-1 of the 5-commit refactor for
// problem #8).
//
// This file owns the Drive adapters + MediaStore derivation + StyleRegistry
// loading for the canonical Google Drive integration. Extracted from
// composition.go so bundle debt is split per AGENTS.md Pattern 5 (1 concept
// per focused file) and BuildDriveBundle's own body remains pure (no
// concurrent goroutine spawns — composition_test.go::
// TestComposition_NoGoroutinesSpawned_FrozenSiteCount).
//
// commit ci/composition-split wave-1 (June 2026): replaced the legacy
// post-ctor setter pair (`mediaStore.SetAssetTree + SetTreeSource`) with
// a single `drive.NewStoreWithOptions(..., drive.StoreOptions{AssetTree,
// TreeSources})` call so the dependency graph lands at the ctor boundary.
package app

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// BuildDriveBundle constructs the Drive adapters + MediaStore + DestResolver.
// Loads StyleRegistry at the top so ensureStyleDriveFolders (called via the
// returned startDriveBackgroundFolders closure) receives the non-nil pointer.
//
// PR9-A (June 2026): BuildDriveBundle returns an IOpaqueStartFunc closure
// that defers side-effecting initialisation (Drive folder validation,
// style-folder pre-creation, storage directory creation) to the lifecycle.
// The bundle itself is fully populated on return.
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

	var driveUploader *drive.Uploader
	var dests *DriveDestinations
	if driveClient != nil {
		driveUploader = &drive.Uploader{Service: driveClient, Log: log}
		dests = resolveRuntimeDestinations(ctx, dbs.main.DB, driveClient, cfg, log)
	} else {
		dests = configOnlyDestinations(cfg)
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
	// startDriveBackgroundFolders (defined below). Package-level function
	// so the source-level goroutine-count freeze test reports zero spawns
	// in BuildDriveBundle's own body.
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
	}, startClosure, nil
}

// startDriveBackgroundFolders performs the side-effecting Drive init that
// was previously inlined in BuildDriveBundle (PR9-A, June 2026). It
// pre-creates style folders on Drive, validates critical Drive folder
// paths, and ensures local storage directories exist.
//
// Lifecycle-runtime-ownership (June 2026): now returns error on required
// folder validation failure. Style folder creation remains async (background
// after readiness passes). Local storage directory creation errors are
// logged as warnings (they are non-fatal).
//
// Invoked by the lifecycle after WireRegistry completes, before the HTTP
// server begins accepting requests.
func startDriveBackgroundFolders(
	ctx context.Context,
	cfg *config.Config,
	driveClient *gdrive.Service,
	driveUploader *drive.Uploader,
	dests *DriveDestinations,
	styleRegistry *generation.StyleRegistry,
	log *zap.Logger,
) error {
	// Style folder pre-creation: async after readiness (optional).
	if driveClient != nil && dests.ImagesFolder() != "" && dests.ImagesFolder() != dests.MediaRoot {
		concurrent.SafeGo("drive-style-folders", func() {
			ensureStyleDriveFolders(ctx, driveUploader, dests.ImagesFolder(), styleRegistry, log)
		})
		log.Info("Style Drive folders using Images root", zap.String("folder_id", dests.ImagesFolder()))
	}

	// Required folder validation: synchronous, returns error on failure.
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

	// Local storage directories: optional (logged as warnings).
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
