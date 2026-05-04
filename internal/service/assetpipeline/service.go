package assetpipeline

import (
	"context"
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/mediaregistry"
)

// Service orchestrates the asset pipeline: hash, upload, and finalize.
type Service struct {
	finalizer *Finalizer
}

// NewService creates a new asset pipeline service.
func NewService(
	uploader *Uploader,
	mediaFinalizer *mediaregistry.Finalizer,
	assetIndex *assetindex.Service,
	log *zap.Logger,
) *Service {
	return &Service{
		finalizer: NewFinalizer(uploader, mediaFinalizer, assetIndex, log),
	}
}

// NewServiceWithDrive creates a new asset pipeline service with a Drive API client.
func NewServiceWithDrive(
	driveSvc *gdrive.Service,
	log *zap.Logger,
	mediaFinalizer *mediaregistry.Finalizer,
	assetIndex *assetindex.Service,
) *Service {
	uploader := NewUploader(driveSvc, log)
	return &Service{
		finalizer: NewFinalizer(uploader, mediaFinalizer, assetIndex, log),
	}
}

// Finalize processes an asset through the pipeline: hash, upload, and register.
func (s *Service) Finalize(ctx context.Context, in *FinalizeInput) (*FinalizeResult, error) {
	return s.finalizer.Finalize(ctx, in)
}

// HashFile computes the file hash for an asset.
func (s *Service) HashFile(path string) (string, error) {
	return HashFile(path)
}

// ContentHashFile computes the content hash for an asset.
func (s *Service) ContentHashFile(path string) (string, error) {
	return ContentHashFile(path)
}

func (s *Service) UploadToDrive(ctx context.Context, localPath, folderID string) (string, string, error) {
	if s.finalizer.uploader != nil {
		return s.finalizer.uploader.Upload(ctx, localPath, folderID)
	}
	return "", "", nil
}
