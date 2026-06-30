package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	imagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/images"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	fullimagessvc "github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)
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
	handler := imagesapi.NewFullImagesHandler(svc)
	// Wave 14 close (June 2026, PR3): the receiver was moved from
	// internal/api/fullimages/ into internal/api/images/ as a sibling
	// of ImagesHandler, but the public URL stays at /fullimages to
	// satisfy the zero-change-contract guarantee (public REST contract
	// is inviolate per the user spec).
	mod := module.NewRouteModule(
		"fullimages",
		func() bool { return cfg.Features.ImagesEnabled },
		"/fullimages",
		handler,
		log,
	)
	log.Info("created FullImages module (handler in api/images/, prefix /fullimages retained for zero-change-contract)")
	return &FullImagesWiring{Handler: handler, Module: mod}, nil
}
