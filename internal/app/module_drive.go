package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"go.uber.org/zap"
)

// DriveWiring holds the Drive module wiring.
type DriveWiring struct {
	Handler   *drive.DriveHandler
	Module    module.Module
	Reconcile *drivecleanup.Service
}

// WireDrive creates the Drive handler and module.
func WireDrive(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*DriveWiring, error) {
	var driveUploader *driveutil.Uploader
	if coreDeps.DriveClient != nil {
		driveUploader = &driveutil.Uploader{Service: coreDeps.DriveClient, Log: log}
	}
	var reconcileSvc *drivecleanup.Service
	if driveUploader != nil {
		reconcileSvc = drivecleanup.NewService()
		log.Info("drive reconcile service initialized")
	}
	handler := drive.NewDriveHandler(reconcileSvc, driveUploader)
	mod := drive.NewModule(cfg, log, handler)
	log.Info("created Drive module")
	return &DriveWiring{Handler: handler, Module: mod, Reconcile: reconcileSvc}, nil
}
