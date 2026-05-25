package assetregistry

import (
	"context"
	"net/http"
	"time"

	driveapi "google.golang.org/api/drive/v3"
	"velox/go-master/internal/storage/drive"
	driveupload "velox/go-master/internal/upload/drive"
)

type DriveVerifier interface {
	VerifyDriveLink(ctx context.Context, driveLink string) (bool, error)
}

type HTTPDriveVerifier struct {
	client *http.Client
}

func NewHTTPDriveVerifier() *HTTPDriveVerifier {
	return &HTTPDriveVerifier{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (v *HTTPDriveVerifier) VerifyDriveLink(ctx context.Context, driveLink string) (bool, error) {
	if driveLink == "" {
		return false, nil
	}

	req, err := http.NewRequestWithContext(ctx, "HEAD", driveLink, nil)
	if err != nil {
		return false, err
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	return resp.StatusCode >= 200 && resp.StatusCode < 300, nil
}

// APIDriveVerifier verifies Drive links using the Google Drive API.
// This is more reliable than HTTP HEAD requests for Google Drive links.
type APIDriveVerifier struct {
	uploader *driveupload.Uploader
}

func NewAPIDriveVerifier(driveSvc *driveapi.Service) *APIDriveVerifier {
	var uploader *driveupload.Uploader
	if driveSvc != nil {
		uploader = &driveupload.Uploader{Service: driveSvc}
	}
	return &APIDriveVerifier{uploader: uploader}
}

func (v *APIDriveVerifier) VerifyDriveLink(ctx context.Context, driveLink string) (bool, error) {
	if driveLink == "" || v.uploader == nil {
		return false, nil
	}

	fileID := drive.FileIDFromLink(driveLink)
	if fileID == "" {
		return false, nil
	}

	return v.uploader.FileExists(ctx, fileID)
}
