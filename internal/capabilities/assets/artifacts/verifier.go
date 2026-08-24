package assets

// DriveVerifier is the application-side port for verifying Google Drive
// links. PR2.7 (June 2026) extracted the SDK-wired concrete
// (formerly APIDriveVerifier) to
// internal/infrastructure/drive/verifier_adapter.go::DriveVerifierAdapter
// because the concrete imported google.golang.org/api/drive/v3 +
// the drive.Uploader adapter — a direct application → infrastructure
// import. The DriveVerifierAdapter in drive/ implements this interface
// and is wired in internal/app/lifecycle.go::NewLifecycleFromDeps.
//
// The cycle that PR2.7 broke (now historical only) was:
//   artlist → assets/artifacts/verifier.go → drive → artlist
// triggered historically by folder_manager.go (in drive pkg) importing
// artlist for the []artlist.DriveFileRef return type on ListByQuery.
// F3.14 (June 2026) retired both the wide-port ListByQuery method AND
// the DriveFileRef type itself (zero remaining direct callers after
// F2.11's brutal-override consolidation routed artlist callers through
// drive.Reader / delivery.Publisher / drive.FileLifecycle per godlike/06
// 'one owner per fact'). The architectural split here remains so future
// port-widening cannot accidentally re-introduce the App->Infra
// back-edge. The new verifier_adapter.go imports artifacts for the
// port interface, but artifacts no longer imports drive; one-direction
// subset keeps Go's import checker happy.
//
// Wave A Item 32 (June 2026): the legacy HTTPDriveVerifier concrete
// (an HTTP-based fallback that did a raw HEAD request to the Drive
// webViewLink URL) is REMOVED. Grep confirmed zero callers of
// NewHTTPDriveVerifier() — the canonical DriveVerifierAdapter (which
// uses Reader.FileIsNotTrashed, semantically better for verification —
// a trashed file should not be considered "verified") is the only
// concrete wired in production. The DriveVerifier interface is retained
// because the artifacts.Finalizer (finalizer.go:86) depends on it via
// the narrow f.driveVerifier field. Adding a new concrete (HTTP-based
// or otherwise) requires re-introducing a composition-root wiring site
// + a one-line typed-port assertion; do not resurrect HTTPDriveVerifier
// in this file.

import "context"

// DriveVerifier is the canonical port for verifying Google Drive links.
// The single production concrete is
// internal/infrastructure/drive.DriveVerifierAdapter, which uses
// Reader.FileIsNotTrashed (semantically better than FileExists because
// a trashed file should not be considered "verified"). Test doubles
// satisfy this interface structurally via Go's implicit-interface
// rules (see finalizer_test.go::mockDriveVerifier).
type DriveVerifier interface {
	VerifyDriveLink(ctx context.Context, driveLink string) (bool, error)
}
