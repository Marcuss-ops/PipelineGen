package main

import (
	"context"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/artlistsync"
	"velox/go-master/internal/catalogsync"
	"velox/go-master/internal/runtime"
	"velox/go-master/internal/stocksync"
	"velox/go-master/internal/watcher"
	"velox/go-master/pkg/config"
)

type SyncDeps struct {
	DriveSync    *stocksync.DriveSync
	ArtlistSync  *artlistsync.ArtlistSync
	CatalogSync  *catalogsync.Sync
	DriveWatcher *watcher.Watcher
}

func initSyncServices(
	cfg *config.Config, log *zap.Logger, clips *ClipDeps, drive *DriveDeps,
) (*SyncDeps, []runtime.BackgroundService, error) {
	driveClient := drive.DriveHandler.GetDriveClient()
	artlistIndexPath := cfg.Storage.DataDir + "/artlist_stock_index.json"

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

	var artlistSyncSvc *artlistsync.ArtlistSync
	if driveClient != nil {
		artlistSyncSvc = artlistsync.NewArtlistSync(
			driveClient,
			cfg.Drive.ArtlistFolderID,
			artlistIndexPath,
		)
		log.Info("ArtlistSync initialized (will be driven by Watcher)")
	}

	var catalogSyncSvc *catalogsync.Sync
	if clips.CatalogDB != nil {
		catalogSyncSvc = catalogsync.New(clips.CatalogDB, clips.StockDB, clips.ClipDB, artlistIndexPath)
		log.Info("CatalogSync bridge initialized")
	}

	var driveWatcher *watcher.Watcher
	var services []runtime.BackgroundService

	if driveClient != nil {
		syncInterval := time.Duration(cfg.DriveSync.Interval) * time.Second
		driveWatcher = watcher.NewWatcher(driveClient, cfg.Drive.StockRootFolderID, syncInterval)

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

		if catalogSyncSvc != nil {
			catalogSyncRef := catalogSyncSvc
			syncTimeout := time.Duration(cfg.DriveSync.SyncTimeout) * time.Second
			driveWatcher.OnCycleComplete(func(ctx context.Context) error {
				syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
				defer cancel()
				if err := catalogSyncRef.Sync(syncCtx); err != nil {
					log.Warn("CatalogSync (Watcher-driven) failed", zap.Error(err))
					return err
				}
				log.Info("CatalogSync (Watcher-driven) completed")
				return nil
			})
		}

		services = append(services, driveWatcher)
		log.Info("Watcher created as Drive polling authority")
	}

	return &SyncDeps{
		DriveSync:    driveSync,
		ArtlistSync:  artlistSyncSvc,
		CatalogSync:  catalogSyncSvc,
		DriveWatcher: driveWatcher,
	}, services, nil
}
