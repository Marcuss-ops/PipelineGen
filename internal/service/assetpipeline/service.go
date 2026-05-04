package assetpipeline

import (
	"context"
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/mediaregistry"
)

type Service struct {
	finalizer *Finalizer
}

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

func (s *Service) Finalize(ctx context.Context, in *FinalizeInput) (*FinalizeResult, error) {
	return s.finalizer.Finalize(ctx, in)
}

func (s *Service) HashFile(path string) (string, error) {
	return HashFile(path)
}

func (s *Service) ContentHashFile(path string) (string, error) {
	return ContentHashFile(path)
}

func (s *Service) UploadToDrive(ctx context.Context, localPath, folderID string) (string, string, error) {
	if s.finalizer.uploader != nil {
		return s.finalizer.uploader.Upload(ctx, localPath, folderID)
	}
	return "", "", nil
}
