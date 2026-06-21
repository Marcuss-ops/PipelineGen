package artifacts

// DriveVerifier is the application-side port for verifying Google Drive
// links. PR2.7: the SDK-wired concrete (formerly APIDriveVerifier in
// this file) was extracted to
// internal/infrastructure/drive/verifier_adapter.go::DriveVerifierAdapter
// because the concrete imported google.golang.org/api/drive/v3 +
// the drive.Uploader adapter — a direct application → infrastructure
// import. After PR2.7: this file holds ONLY the port interface +
// HTTPDriveVerifier (an HTTP-based fallback that doesn't import any
// SDK or infrastructure package). The DriveVerifierAdapter in drive/
// implements this interface and is wired in
// internal/app/lifecycle.go::NewLifecycleFromDeps.
//
// The cycle that PR2.7 broke was:
//   artlist → assets/artifacts/verifier.go → drive → artlist
// triggered by folder_manager.go (in drive pkg) importing artlist for
// the []artlist.DriveFileRef return type on ListByQuery. Moving
// APIDriveVerifier to drive/ is a one-direction ⊆ (the new
// verifier_adapter.go imports artifacts for the port interface, but
// artifacts no longer imports drive) so Go's import checker accepts it.

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
