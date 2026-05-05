package assetpipeline

import (
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/mediaregistry"
)

// NewFinalizerWithDrive creates a new asset pipeline finalizer with a Drive API client.
func NewFinalizerWithDrive(
	driveSvc *gdrive.Service,
	log *zap.Logger,
	mediaFinalizer *mediaregistry.Finalizer,
	assetIndex *assetindex.Service,
) *Finalizer {
	uploader := NewUploader(driveSvc, log)
	return NewFinalizer(uploader, mediaFinalizer, assetIndex, log)
}
