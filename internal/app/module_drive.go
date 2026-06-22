package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
)

// DriveWiring holds the Drive module wiring.
type DriveWiring struct {
	Handler   *drive.DriveHandler
	Module    module.Module
	Reconcile *drivecleanup.Service
}

// WireDrive creates the Drive handler and module.
//
// PR4d-chunk1 (June 2026): narrow bundle signature. Takes the canonical
// Google Drive *gdrive.Service directly — sourced from root.Drive.DriveClient
// in WireRegistry. Zero *CoreDeps dependency.
func WireDrive(cfg *config.Config, log *zap.Logger, driveClient *gdrive.Service) (*DriveWiring, error) {
	var driveUploader *driveup.Uploader
	if driveClient != nil {
		driveUploader = &driveup.Uploader{Service: driveClient, Log: log}
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
