package bootstrap

import (
	"context"

	"go.uber.org/zap"

	"velox/go-master/internal/service/clipcatalog"
	"velox/go-master/internal/service/clipindexer"
	"velox/go-master/pkg/config"
)

func wireArtlistCatalog(cfg *config.Config, coreDeps *CoreDeps, log *zap.Logger) (*clipcatalog.Repository, *clipindexer.Service) {
	if coreDeps.ClipIndexerService != nil {
		clipCatalogRepo := clipcatalog.NewRepository(coreDeps.ArtlistDB.DB, log)
		clipCatalogRepo.SetServerInfo(cfg.ClipIndexer.ServerURL, coreDeps.ArtlistDB.Path())
		return clipCatalogRepo, coreDeps.ClipIndexerService
	}

	if coreDeps.ArtlistDB != nil && coreDeps.ArtlistDB.DB != nil {
		if err := clipcatalog.EnsureSchema(context.Background(), coreDeps.ArtlistDB.DB, log); err != nil {
			log.Warn("failed to ensure clipcatalog schema", zap.Error(err))
		}
	}

	clipCatalogRepo := clipcatalog.NewRepository(coreDeps.ArtlistDB.DB, log)
	clipCatalogRepo.SetServerInfo(cfg.ClipIndexer.ServerURL, coreDeps.ArtlistDB.Path())

	clipIndexerSvc := clipindexer.NewService(&clipindexer.Config{
		Enabled:               cfg.ClipIndexer.Enabled,
		ServerURL:             cfg.ClipIndexer.ServerURL,
		ScriptPath:            cfg.ClipIndexer.ScriptPath,
		PythonBin:             cfg.ClipIndexer.PythonBin,
		AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist,
		DBPath:                coreDeps.ArtlistDB.Path(),
	}, coreDeps.ArtlistDB.DB, coreDeps.ArtlistDB.Path(), log)

	// Start background embedding server and watchdog
	if err := clipIndexerSvc.StartServer(); err != nil {
		log.Warn("failed to start embedding server", zap.Error(err))
	} else {
		clipIndexerSvc.StartWatchdog()
	}

	return clipCatalogRepo, clipIndexerSvc
}
