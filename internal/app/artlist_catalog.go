package app

import (
	"context"

	"go.uber.org/zap"

	"velox/go-master/internal/config"
	"velox/go-master/internal/media/clipcatalog"
	"velox/go-master/internal/media/clipindexer"
)

func wireArtlistCatalog(ctx context.Context, cfg *config.Config, coreDeps *CoreDeps, log *zap.Logger) (*clipcatalog.Repository, *clipindexer.Service) {
	if coreDeps.ClipIndexerService != nil {
		clipCatalogRepo := clipcatalog.NewRepository(coreDeps.MediaDB.DB, log)
		clipCatalogRepo.SetServerInfo(cfg.ClipIndexer.ServerURL, coreDeps.MediaDB.Path())
		return clipCatalogRepo, coreDeps.ClipIndexerService
	}

	if coreDeps.MediaDB != nil && coreDeps.MediaDB.DB != nil {
		if err := clipcatalog.EnsureSchema(ctx, coreDeps.MediaDB.DB, log); err != nil {
			log.Warn("failed to ensure clipcatalog schema", zap.Error(err))
		}
	}

	clipCatalogRepo := clipcatalog.NewRepository(coreDeps.MediaDB.DB, log)
	clipCatalogRepo.SetServerInfo(cfg.ClipIndexer.ServerURL, coreDeps.MediaDB.Path())

	clipIndexerSvc := clipindexer.NewService(&clipindexer.Config{
		Enabled:               cfg.ClipIndexer.Enabled,
		ServerURL:             cfg.ClipIndexer.ServerURL,
		ScriptPath:            cfg.ClipIndexer.ScriptPath,
		PythonBin:             cfg.ClipIndexer.PythonBin,
		AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist,
		DBPath:                coreDeps.MediaDB.Path(),
	}, coreDeps.MediaDB.DB, coreDeps.MediaDB.Path(), log)

	// Start background embedding server and watchdog
	if err := clipIndexerSvc.StartServer(ctx); err != nil {
		log.Warn("failed to start embedding server", zap.Error(err))
	} else {
		clipIndexerSvc.StartWatchdog(ctx)
	}

	return clipCatalogRepo, clipIndexerSvc
}
