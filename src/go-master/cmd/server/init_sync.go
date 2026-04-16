package main

import (
	"context"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/artlistsync"
	"velox/go-master/internal/runtime"
	"velox/go-master/internal/stocksync"
	"velox/go-master/internal/watcher"
	"velox/go-master/pkg/config"
)

// SyncDeps holds the background sync and watcher services.
type SyncDeps struct {
	DriveSync    *stocksync.DriveSync
	ArtlistSync  *artlistsync.ArtlistSync
	DriveWatcher *watcher.Watcher
}

// initSyncServices initializes the synchronization services.
//
// Key architectural change: The Watcher is now the SOLE component that polls
// the Drive API. DriveSync and ArtlistSync are registered as OnCycleComplete
// callbacks on the Watcher, so they execute whenever the Watcher completes a
// polling cycle. This eliminates the previous triple-polling problem where
// DriveSync, ArtlistSync, and Watcher all independently polled Drive.
//
// Background services are returned for registration with the ServiceGroup —
// they are NOT started here.
func initSyncServices(
	cfg *config.Config, log *zap.Logger, clips *ClipDeps, drive *DriveDeps,
) (*SyncDeps, []runtime.BackgroundService, error) {
	driveClient := drive.DriveHandler.GetDriveClient()

	// === DriveSync (created, NOT auto-started — Watcher drives it) ===
	var driveSync *stocksync.DriveSync
	if clips.StockDB != nil && driveClient != nil {
		if clips.ClipDB != nil {
			driveSync = stocksync.NewDriveSyncWithClips(driveClient, clips.StockDB, clips.ClipDB, cfg.Drive.StockRootFolderID)
			log.Info("DriveSync initialized with separate StockDB + ClipDB")
		} else {
			driveSync = stocksync.NewDriveSync(driveClient, clips.StockDB, cfg.Drive.StockRootFolderID)
			log.Info("DriveSync initialized with StockDB only")
		}
	}

	// === ArtlistSync (created, NOT auto-started — Watcher drives it) ===
	var artlistSyncSvc *artlistsync.ArtlistSync
	if driveClient != nil {
		artlistSyncSvc = artlistsync.NewArtlistSync(
			driveClient,
			cfg.Drive.ArtlistFolderID,
			cfg.Storage.DataDir+"/artlist_stock_index.json",
		)
		log.Info("ArtlistSync initialized (will be driven by Watcher)")
	}

	// === Watcher — the single Drive polling authority ===
	var driveWatcher *watcher.Watcher
	var services []runtime.BackgroundService

	if driveClient != nil {
		driveWatcher = watcher.NewWatcher(driveClient, cfg.Drive.StockRootFolderID)

		// Register DriveSync as a Watcher callback
		if driveSync != nil {
			driveSyncRef := driveSync
			syncTimeout := time.Duration(cfg.DriveSync.SyncTimeout) * time.Second
			driveWatcher.OnCycleComplete(func(ctx context.Context) error {
				syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
				defer cancel()
				if err := driveSyncRef.Sync(syncCtx); err != nil {
					log.Warn("DriveSync (Watcher-driven) failed", zap.Error(err))
					return err
				}
				log.Info("DriveSync (Watcher-driven) completed")
				return nil
			})
		}

		// Register ArtlistSync as a Watcher callback
		if artlistSyncSvc != nil {
			artlistSyncRef := artlistSyncSvc
			syncTimeout := time.Duration(cfg.DriveSync.SyncTimeout) * time.Second
			driveWatcher.OnCycleComplete(func(ctx context.Context) error {
				syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
				defer cancel()
				if err := artlistSyncRef.Sync(syncCtx); err != nil {
					log.Warn("ArtlistSync (Watcher-driven) failed", zap.Error(err))
					return err
				}
				log.Info("ArtlistSync (Watcher-driven) completed")
				return nil
			})
		}

		// Register Watcher as a BackgroundService (native implementation)
		services = append(services, driveWatcher)
		log.Info("Watcher created as Drive polling authority (drives DriveSync + ArtlistSync)")
	}

	return &SyncDeps{
		DriveSync:    driveSync,
		ArtlistSync:  artlistSyncSvc,
		DriveWatcher: driveWatcher,
	}, services, nil
}
