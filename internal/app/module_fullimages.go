package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	imagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/images"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	fullimagessvc "github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"go.uber.org/zap"
)

// FullImagesWiring holds the FullImages module wiring.
//
// PR3 (June 2026): Wave 14 close. The handler was moved from
// `internal/api/fullimages/` to `internal/api/images/` as a sibling
// of ImagesHandler. The route prefix stays `/fullimages` (NOT
// `/images`) so the public REST URL stays unchanged — zero-change-
// contract per PR3 spec. The sub-path `/video/generate` is unchanged
// (no collision with `ImagesHandler.Generate` which mounts at
// `/generate` under the `/images` prefix).
type FullImagesWiring struct {
	Handler *imagesapi.FullImagesHandler
	Module  module.Module
}

// WireFullImages creates the FullImages handler and module.
//
// PR4d-chunk1 (June 2026): narrow bundle signature. Takes the canonical
// ImageService + MediaStore directly — sourced from root.Domains.ImageService
// and root.Drive.MediaStore in WireRegistry. Zero *CoreDeps dependency.
//
// PR3 (June 2026): Wave 14 close moved the receiver into
// `internal/api/images/` (the handler package), but the route prefix
// stays /fullimages (zero-change-contract — the URL must stay at
// /api/fullimages/video/generate). The new module path is co-located
// with ImagesHandler internally but the public REST contract is
// unchanged.
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
