package app

import (
	"go.uber.org/zap"
	"velox/go-master/internal/media/fullimages"
	imgservice "velox/go-master/internal/media/images"
)

// newFullImagesService creates the FullImagesService used by the wiring.
// It wraps the existing image service with the section-based generation logic.
func newFullImagesService(imgSvc *imgservice.Service, log *zap.Logger) *fullimages.Service {
	return fullimages.NewService(imgSvc, log)
}
