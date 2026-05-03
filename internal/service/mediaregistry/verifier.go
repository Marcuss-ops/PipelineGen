package mediaregistry

import (
	"context"
	"net/http"
	"time"
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
