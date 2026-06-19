package app

import (
	drivehandler "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/database/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"

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
	// Create drive uploader
	var driveUploader *drive.Uploader
	if coreDeps.DriveClient != nil {
		driveUploader = &drive.Uploader{Service: coreDeps.DriveClient, Log: log}
	}

	// Create drive reconcile service
	var reconcileSvc *drivecleanup.Service
	if driveUploader != nil {
		reconcileSvc = drivecleanup.NewService()
		log.Info("drive reconcile service initialized")
	}

	handler := drivehandler.NewHandler(reconcileSvc, driveUploader)
	mod := module.NewDriveModule(cfg, log, handler)
	log.Info("created Drive module")

	return &DriveWiring{
		Handler:   handler,
		Module:    mod,
		Reconcile: reconcileSvc,
	}, nil
}
