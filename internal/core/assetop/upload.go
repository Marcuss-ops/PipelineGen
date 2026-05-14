package assetop

import (
	"context"
	"path/filepath"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
	"velox/go-master/internal/upload/drive"
)

type Uploader struct {
	driveSvc *gdrive.Service
	log      *zap.Logger
}

func NewUploader(driveSvc *gdrive.Service, log *zap.Logger) *Uploader {
	return &Uploader{
		driveSvc: driveSvc,
		log:      log,
	}
}

func (u *Uploader) Upload(ctx context.Context, localPath, folderID string) (driveLink string, downloadLink string, fileID string, err error) {
	if u.driveSvc == nil || folderID == "" {
		return "", "", "", nil
	}

	filename := filepath.Base(localPath)

	uploader := &drive.Uploader{
		Service: u.driveSvc,
		Log:     u.log,
	}

	result, err := uploader.UploadFile(ctx, localPath, folderID, filename)
	if err != nil {
		return "", "", "", err
	}

	return result.WebViewLink, "https://drive.google.com/uc?id=" + result.FileID, result.FileID, nil
}
