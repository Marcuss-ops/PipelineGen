package bootstrap

import (
	"go.uber.org/zap"
	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	artlistPkg "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/clipresolver"
	"velox/go-master/pkg/config"
)

func wireArtlistHandler(
	cfg *config.Config,
	artlistSvc *artlistPkg.Service,
	coreDeps *CoreDeps,
	clipResolver *clipresolver.Service,
	presetsConfig *artlistPkg.PresetsConfig,
	log *zap.Logger,
) *artlistHandler.Handler {
	if artlistSvc == nil {
		return nil
	}
	return artlistHandler.NewHandler(
		artlistSvc,
		coreDeps.CatalogSyncService,
		coreDeps.JobsService,
		clipResolver,
		cfg.Paths.NodeScraperDir,
		log,
		presetsConfig,
		cfg,
	)
}
