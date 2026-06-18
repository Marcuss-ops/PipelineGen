package app

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
)

type DriveDestinations struct {
	MediaRoot        string
	VideoAIRoot      string
	SoundEffectsRoot string
	imagesFolder     string
	videoAIFolder    string
}

func (d *DriveDestinations) RootFolder() string {
	return d.MediaRoot
}

func (d *DriveDestinations) ImagesFolder() string {
	return d.imagesFolder
}

func (d *DriveDestinations) VideoAIFolder() string {
	return d.videoAIFolder
}

func resolveRuntimeDestinations(ctx context.Context, db *sql.DB, driveClient *gdrive.Service, cfg *config.Config, log *zap.Logger) *DriveDestinations {
	return &DriveDestinations{
		MediaRoot:        cfg.Drive.RootFolder(),
		VideoAIRoot:      cfg.Drive.VideoAIRootFolder,
		SoundEffectsRoot: cfg.Drive.SoundEffectsRootFolder,
		imagesFolder:     cfg.Drive.ImagesFolder(),
		videoAIFolder:    cfg.Drive.VideoAIFolder(),
	}
}

func configOnlyDestinations(cfg *config.Config) *DriveDestinations {
	return &DriveDestinations{
		MediaRoot:        cfg.Drive.RootFolder(),
		VideoAIRoot:      cfg.Drive.VideoAIRootFolder,
		SoundEffectsRoot: cfg.Drive.SoundEffectsRootFolder,
		imagesFolder:     cfg.Drive.ImagesFolder(),
		videoAIFolder:    cfg.Drive.VideoAIFolder(),
	}
}

