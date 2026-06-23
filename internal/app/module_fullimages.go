package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	fullimageshandler "github.com/Marcuss-ops/PipelineGen/internal/api/fullimages"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	fullimagessvc "github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
	"go.uber.org/zap"
)

// FullImagesWiring holds the FullImages module wiring.
type FullImagesWiring struct {
	Handler *fullimageshandler.FullImagesHandler
	Module  module.Module
}

// WireFullImages creates the FullImages handler and module.
//
// PR4d-chunk1 (June 2026): narrow bundle signature. Takes the canonical
// ImageService + MediaStore directly — sourced from root.Domains.ImageService
// and root.Drive.MediaStore in WireRegistry. Zero *CoreDeps dependency.
func WireFullImages(cfg *config.Config, log *zap.Logger, imageSvc *imgservice.Service, mediaStore *driveup.Store) (*FullImagesWiring, error) {
	if imageSvc == nil {
		log.Warn("fullimages: ImageService not available, skipping module")
		return nil, nil
	}
	svc := fullimagessvc.NewService(
		imageSvc,
		ffmpeg.NewFromConfig(cfg),
		mediaStore,
		cfg.Storage.ImagesPath(),
		log,
	)
	handler := fullimageshandler.NewFullImagesHandler(svc)
	mod := module.NewRouteModule(
		"fullimages",
		func() bool { return cfg.Features.ImagesEnabled },
		"/fullimages",
		handler,
		log,
	)
	log.Info("created FullImages module using RouteModule")
	return &FullImagesWiring{Handler: handler, Module: mod}, nil
}
