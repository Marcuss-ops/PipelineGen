// Package drive — verifier_adapter.go (PR2.7)
//
// DriveVerifierAdapter is the infrastructure-side implementation of the
// artifacts.DriveVerifier port. It is functionally equivalent to the
// legacy `APIDriveVerifier` that lived in
// internal/application/assets/artifacts/verifier.go — PR2.7 moved it
// here to break the App → Infra back-edge of the import cycle:
//
//	artlist (folder_manager.go) ← artlist port for DriveFileRef
//	    ↑                                                   │
//	    │                                                   ▼
//	verifier.go (legacy APIDriveVerifier) → drive.Uploader → driveapi.Service
//
// The flow used to be:
//
//	artlist → assets/artifacts/verifier.go → drive → artlist
//
// which Go rejected as a cycle once folder_manager.go started importing
// W16-PR4: port now returns `[]drivepkg.DriveFileRef` (the prior
// application-layer alias was removed; the canonical name resolves
// from internal/infrastructure/drive.DriveFileRef).

// Fix: strip the concrete out of verifier.go (which now holds ONLY the
// `DriveVerifier` interface + `HTTPDriveVerifier` HTTP-based fallback),
// and place the SDK-wired concrete here in infrastructure where the SDK
// is naturally referenced. The verifier port stays in the application
// layer per the AGENTS.md "Application owns interfaces" rule.
package drive

import (
	"context"

	driveapi "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
)

// DriveVerifierAdapter verifies Google Drive links via the Drive API.
// More reliable than HTTP HEAD requests for Google Drive links because
// the API checks file ID existence + not-trashed state.
type DriveVerifierAdapter struct {
	uploader *Uploader
}

// NewDriveVerifierAdapter constructs the adapter from a configured
// Drive SDK service. Composition root in `internal/app/lifecycle.go`
// wires this where the legacy `artifacts.NewAPIDriveVerifier` was
// called (the surface swap is mechanical — same input, same port
// interface, just labelled an "adapter" instead of a "verifier").
func NewDriveVerifierAdapter(driveSvc *driveapi.Service) *DriveVerifierAdapter {
	var uploader *Uploader
	if driveSvc != nil {
		uploader = &Uploader{Service: driveSvc}
	}
	return &DriveVerifierAdapter{uploader: uploader}
}

// VerifyDriveLink returns true when the Drive link points to a file
// that exists AND is not in the trash. Empty link returns (false, nil)
// — callers handle "no link" as a not-found state without surfacing
// an error. The port returns (bool, error) where nil error + false bool
// means "link is empty or syntactically invalid", and a non-nil error
// is reserved for transport failures.
func (v *DriveVerifierAdapter) VerifyDriveLink(ctx context.Context, driveLink string) (bool, error) {
	if driveLink == "" || v.uploader == nil {
		return false, nil
	}

	fileID := FileIDFromLink(driveLink)
	if fileID == "" {
		return false, nil
	}

	return v.uploader.FileExists(ctx, fileID)
}

// Compile-time assertion: DriveVerifierAdapter satisfies the
// artifacts.DriveVerifier port. Importing artifacts directly is
// safe here (drive → artifacts): artifacts does NOT import drive anymore
// (PR2.7 stripped the verifier.go's drive + driveupload + driveapi
// imports). One-direction ⊆ cyclic-free.
var _ artifacts.DriveVerifier = (*DriveVerifierAdapter)(nil)
