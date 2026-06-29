// Package app — Drive runtime startup validation (FASE 2.B PR1, June 2026).
//
// Originally lived in build_bundles_process.go (then called build_bundles_process)
// as the `startDriveBackgroundFolders` function — the side-effecting
// initialization that the canonical BuildDriveBundle closure captures as
// its IOpaqueStartFunc. PR1 relocates it here so the Drive construction
// (build_bundles_drive.go) and Drive runtime startup (this file) live in
// dedicated files per AGENTS.md Pattern 5.
// PR1 is MOVE-only: zero logic changes, zero call-site changes — the
// signature is preserved so BuildDriveBundle's pointer-capture via
// startClosure in build_bundles_drive.go still resolves via the package
// symbol table.
//
// Cross-references:
//   - internal/app/build_bundles_drive.go: BuildDriveBundle's
//     startClosure invokes this function by name.
//   - internal/app/composition.go: callers of the returned
//     IOpaqueStartFunc (e.g. serverLifecycle.Start) consume the error
//     path on required-folder validation failure (preferred to the
//     pre-PR9-A silently-swallowed warning).
package app

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

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
//
// FASE 2.B PR1 (June 2026): relocated from build_bundles_process.go to
// this dedicated file. Signature + body + behavior are PRESERVED —
// only the doc header was updated to reflect the new ownership and
// PR1 history.
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
