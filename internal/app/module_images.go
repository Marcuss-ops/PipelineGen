package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/images"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"go.uber.org/zap"
)

// ImagesWiring holds the Images module wiring.
type ImagesWiring struct {
	Handler *images.ImagesHandler
	Module  module.Module
}

// WireImages creates the Images handler and module.
//
// PR4d-chunk1 (June 2026): narrow bundle signature. Takes the canonical
// ImageService directly — sourced from root.Domains.ImageService in
// WireRegistry. Zero *CoreDeps dependency. Sets the precedent for the
// 5 remaining Wire<Module>() migrations in PR4d-chunk2.
func WireImages(cfg *config.Config, log *zap.Logger, imageSvc *imgservice.Service) (*ImagesWiring, error) {
	handler := images.NewImagesHandler(imageSvc)
	mod := images.NewModule(cfg, log, handler)
	log.Info("created Images module using api/images")
	return &ImagesWiring{Handler: handler, Module: mod}, nil
}
