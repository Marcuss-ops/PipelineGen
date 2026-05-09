package bootstrap

import (
	"go.uber.org/zap"
	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/core/lifecycle"
	artlistPkg "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/clipindexer"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
)

func wireArtlistService(
	cfg *config.Config,
	coreDeps *CoreDeps,
	artlistLifecycle *lifecycle.Service,
	assetDestResolver destination.Resolver,
	clipIndexerSvc *clipindexer.Service,
	log *zap.Logger,
) (*artlistPkg.Service, error) {
	artlistSvc, err := artlistPkg.NewService(
		cfg,
		coreDeps.DB.DB,
		coreDeps.ArtlistDB.DB,
		cfg.Paths.NodeScraperDir,
		coreDeps.ArtlistRepo,
		coreDeps.MediaProcessor,
		artlistLifecycle,
		assetDestResolver,
		clipIndexerSvc,
		coreDeps.JobsService,
		coreDeps.DriveClient,
		log,
	)

	if err != nil {
		return nil, err
	}

	// Register artlist job handler
	if artlistSvc != nil && coreDeps.JobsService != nil {
		coreDeps.JobsService.RegisterHandler(models.JobTypeArtlistRun, artlistSvc.HandleJob)
		log.Info("registered artlist job handler")
	}

	return artlistSvc, nil
}
