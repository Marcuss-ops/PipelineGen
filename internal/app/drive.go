package app

import (
	drivehandler "velox/go-master/internal/api/handlers/drive"
	"velox/go-master/internal/config"
	"velox/go-master/internal/module"
	"velox/go-master/internal/storage/drivecleanup"

	"go.uber.org/zap"
)

// DriveWiring holds the Drive module wiring
type DriveWiring struct {
	Handler   *drivehandler.Handler
	Module    module.Module
	Reconcile *drivecleanup.Service
}

// WireDrive creates the Drive handler and module
func WireDrive(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*DriveWiring, error) {
	// Create drive reconcile service
	var reconcileSvc *drivecleanup.Service
	if coreDeps.DriveClient != nil {
		reconcileSvc = drivecleanup.NewService(coreDeps.ArtlistRepo, coreDeps.DriveClient, log, true)
		log.Info("drive reconcile service initialized")
	}

	handler := drivehandler.NewHandler(reconcileSvc)
	mod := module.NewDriveModule(cfg, log, handler)
	log.Info("created Drive module")

	return &DriveWiring{
		Handler:   handler,
		Module:    mod,
		Reconcile: reconcileSvc,
	}, nil
}
