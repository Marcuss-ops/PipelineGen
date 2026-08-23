package drive_test

import (
	"testing"

	delivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	platformdelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

// Compile-time assertion: DriveFolderManagerAdapter satisfies the
// narrow drive.FolderManagerPort (the F2.11 / F3.14 end-state surface
// that delivery.Publisher consumes). If a method is added to the port
// (or a parameter renamed) without updating the adapter, this line
// fails to compile, catching the drift at PR review time rather than
// at runtime when a Publish call panics.
//
// F3.14 (June 2026): the previous assertion referenced the
// artlist.DriveFolderManager wide port retired in F2.11 — that
// annotation compiled only because F2.11 left the tombstone comment
// block in artlist/ports.go but did NOT keep the interface declaration
// as a Go type. The PR was therefore masked behind the parallel-agent
// `clip_metadata_writer.go` build breakage. This file replaces the
// dead reference with the canonical narrow port assertion.
//
// F3.14: the FolderManagerPort surface is intentionally single-method
// (EnsureFolder), so the assertion also implicitly pins the F3.14
// commitment that the adapter will not regress to a wide-port surface.
// If a future commit restores ListByQuery / Download / Upload on this
// adapter, it MUST either (a) add the method to the FolderManagerPort
// interface with a deliberate consumer (forcing a delivery.Publisher
// audit), or (b) route the new method through drive.Reader /
// delivery.Publisher / drive.FileLifecycle per godlike/06 "one owner
// per fact".
//
// The assertion uses the external test package pattern (`drive_test`)
// so the test can import `drive` (for both the adapter type and the
// FolderManagerPort interface declared in publisher.go) without an
// import cycle. Production graph remains acyclic.
// Production FolderManagerPort (wide — used by delivery.Publisher for
// nested-folder creation):
var _ drivepkg.FolderManagerPort = (*drivepkg.DriveFolderManagerAdapter)(nil)

// P1.3 (July 2026): also pins the narrow StartupRootsProbe port
// (declared in the delivery package, import direction is
// drive → delivery which is already established by drive/publisher.go).
// Future drift that removes ProbeFolderAccess from this adapter
// without updating platformdelivery.StartupRootsProbe surfaces as a build
// failure here, not a runtime panic at first ValidateDriveRoots
// call site. The two assertions side-by-side catch drift on either
// the wide surface (EnsureFolder + ProbeFolderAccess) or the narrow
// validator surface (ProbeFolderAccess-only).
var _ platformdelivery.StartupRootsProbe = (*drivepkg.DriveFolderManagerAdapter)(nil)

// TestDriveFolderManagerAdapterImplementsFolderManagerPort makes the
// compile-time assertion visible as a `go test` pass line, which is
// useful for CI grep checks ("all PRs touching drive/ pass the
// port-satisfaction guard"). The assertion already fails at compile
// time if the contract is broken, so this test body is intentionally
// trivial.
func TestDriveFolderManagerAdapterImplementsFolderManagerPort(t *testing.T) {
	var _ drivepkg.FolderManagerPort = (*drivepkg.DriveFolderManagerAdapter)(nil)
	var _ platformdelivery.StartupRootsProbe = (*drivepkg.DriveFolderManagerAdapter)(nil)
}
