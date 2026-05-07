package bootstrap

import (
	drivehandler "velox/go-master/internal/api/handlers/drive"
	"velox/go-master/internal/service/drivereconcile"
	"velox/go-master/internal/module"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// DriveWiring holds the Drive module wiring
type DriveWiring struct {
	Handler  *drivehandler.Handler
	Module   module.Module
	Reconcile *drivereconcile.Service
}

// WireDrive creates the Drive handler and module
func WireDrive(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*DriveWiring, error) {
	// Create drive reconcile service
	var reconcileSvc *drivereconcile.Service
	if coreDeps.DriveClient != nil {
		reconcileSvc = drivereconcile.NewService(coreDeps.ArtlistRepo, coreDeps.DriveClient, log)
		log.Info("drive reconcile service initialized")
	}

	handler := drivehandler.NewHandler(reconcileSvc)
	mod := module.NewDriveModule(cfg, log, handler)
	log.Info("created Drive module")

	return &DriveWiring{
		Handler:  handler,
		Module:   mod,
		Reconcile: reconcileSvc,
	}, nil
}
