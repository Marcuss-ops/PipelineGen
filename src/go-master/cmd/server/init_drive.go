package main

import (
	"context"

	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers"
	"velox/go-master/pkg/config"
)

// DriveDeps holds the Google Drive/Docs client and handler.
type DriveDeps struct {
	DriveHandler *handlers.DriveHandler
}

// initDrive initializes the Google Drive handler and its OAuth client.
func initDrive(cfg *config.Config, log *zap.Logger) (*DriveDeps, CleanupFunc, error) {
	driveHandler := handlers.NewDriveHandler(cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err := driveHandler.InitClient(context.Background()); err != nil {
		log.Warn("Drive client failed", zap.Error(err))
	}

	return &DriveDeps{
		DriveHandler: driveHandler,
	}, nil, nil
}
