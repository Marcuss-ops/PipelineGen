package drive_test

import (
	"testing"

	artlistpkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// Compile-time assertion: DriveFolderManagerAdapter satisfies the
// artlist.DriveFolderManager port. If a method is added to the port
// (or a parameter renamed) without updating the adapter, this line
// fails to compile, catching the drift at PR review time rather than
// at runtime when an artlist operation panics.
//
// The assertion uses the external test package pattern (`drive_test`)
// so the test can import both `artlist` (for the port interface type)
// and `drive` (for the adapter type). PR2.7 closed the cycle via Go
// type alias: artlist.DriveFileRef is `type ... = drivepkg.DriveFileRef`,
// so the port interface returning []DriveFileRef resolves at compile
// time through the alias — folder_manager.go never imports artlist
// (the previous cycle aggregator). Production graph remains acyclic.
// Test files can import both freely under `_test.go` separation rules.
var _ artlistpkg.DriveFolderManager = (*drivepkg.DriveFolderManagerAdapter)(nil)

// TestDriveFolderManagerAdapterImplementsArtlistPort makes the
// compile-time assertion visible as a `go test` pass line, which is
// useful for CI grep checks ("all PRs touching Drive assets pass the
// port-satisfaction guard"). The assertion already fails at compile
// time if the contract is broken, so this test body is intentionally
// trivial.
func TestDriveFolderManagerAdapterImplementsArtlistPort(t *testing.T) {
	var _ artlistpkg.DriveFolderManager = (*drivepkg.DriveFolderManagerAdapter)(nil)
}
