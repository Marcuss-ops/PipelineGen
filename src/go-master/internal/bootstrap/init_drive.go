package bootstrap

import (
	"context"

	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers/drive"
	"velox/go-master/pkg/config"
)

// DriveDeps holds the Google Drive/Docs client and handler.
type DriveDeps struct {
	DriveHandler *drive.DriveHandler
}

// initDrive initializes the Google Drive handler and its OAuth client.
func initDrive(cfg *config.Config, log *zap.Logger) (*DriveDeps, CleanupFunc, error) {
	log.Info("Initializing Drive handler", zap.String("creds", cfg.GetCredentialsPath()), zap.String("token", cfg.GetTokenPath()))
	driveHandler := drive.NewDriveHandler(cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err := driveHandler.InitClient(context.Background()); err != nil {
		log.Warn("Drive client failed", zap.Error(err))
	} else {
		log.Info("Drive client initialized successfully")
	}

	return &DriveDeps{
		DriveHandler: driveHandler,
	}, nil, nil
}
