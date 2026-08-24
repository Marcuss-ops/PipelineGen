// Package drive — verifier_adapter.go (PR2.7, updated FASE 9 Step 7)
//
// DriveVerifierAdapter is the infrastructure-side implementation of the
// artifacts.DriveVerifier port. It is functionally equivalent to the
// legacy `APIDriveVerifier` that lived in
// internal/application/assets/artifacts/verifier.go — PR2.7 moved it
// here to break the App → Infra back-edge of the import cycle that the
// drive <-> artlist cycle had historically introduced (folder_manager.go
// once imported artlist for `[]artlist.DriveFileRef` return type on
// ListByQuery). F2.11 + F3.14 (June 2026) closed that cycle by retiring
// the wide DriveFolderManager port and its ListByQuery / Download /
// Upload dead-code surface; this architectural split remains so future
// port-widening cannot accidentally re-introduce the App→Infra back-edge.
//
// Fix: strip the concrete out of verifier.go (which now holds ONLY the
// `DriveVerifier` interface; the HTTPDriveVerifier HTTP-based fallback
// was retired in Wave A Item 32), and place the SDK-wired concrete
// here in infrastructure where the SDK is naturally referenced. The
// verifier port stays in the application layer per the AGENTS.md
// "Application owns interfaces" rule.
//
// FASE 9 Step 7 (June 2026): migrated from *driveapi.Service to Reader
// port. Uses FileIsNotTrashed instead of FileExists — semantically better
// for verification (a trashed file should not be considered "verified").
package drive

import (
	"context"
)

// DriveVerifierAdapter verifies Google Drive links via the Drive API.
// More reliable than HTTP HEAD requests for Google Drive links because
// the API checks file ID existence + not-trashed state.
type DriveVerifierAdapter struct {
	reader Reader
}

// NewDriveVerifierAdapter constructs the adapter from a configured
// Reader port. Composition root in `internal/app/lifecycle.go`
// wires this where the legacy `artifacts.NewAPIDriveVerifier` was
// called.
func NewDriveVerifierAdapter(reader Reader) *DriveVerifierAdapter {
	return &DriveVerifierAdapter{reader: reader}
}

// VerifyDriveLink returns true when the Drive link points to a file
// that exists AND is not in the trash. Empty link returns (false, nil)
// — callers handle "no link" as a not-found state without surfacing
// an error. The port returns (bool, error) where nil error + false bool
// means "link is empty or syntactically invalid", and a non-nil error
// is reserved for transport failures.
func (v *DriveVerifierAdapter) VerifyDriveLink(ctx context.Context, driveLink string) (bool, error) {
	if driveLink == "" || v.reader == nil {
		return false, nil
	}

	fileID := FileIDFromLink(driveLink)
	if fileID == "" {
		return false, nil
	}

	return v.reader.FileIsNotTrashed(ctx, fileID)
}

// Compile-time pin (Pattern 0): local anonymous interface avoids the
// artifacts → assetindex → sqlite/assets → drive → artifacts cycle.
// Full-port assertion enforced at the composition root.
//
// NOTE: if artifacts.DriveVerifier gains a second method, this local
// assertion won't catch the drift — the composition-root pin will.
var _ interface {
	VerifyDriveLink(ctx context.Context, driveLink string) (bool, error)
} = (*DriveVerifierAdapter)(nil)
