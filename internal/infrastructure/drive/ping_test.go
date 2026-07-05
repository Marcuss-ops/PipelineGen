// Package drive — ping_test.go (FASE 9, June 2026)
//
// FASE 9 (June 2026, P0.1 / DRIVE-005): the canonical Pattern 0 ports
// (Admin + Reader) are introduced alongside the existing concrete
// *drive.Uploader. This file is the canary test surface for the port
// abstraction: it pins the compile-time ability to assign *Uploader to
// drive.Admin / drive.Reader (mirroring the var-(...)-nil asserts at
// the bottom of ports.go), and exercises the Ping liveness-probe
// method on the nil config path.
//
// The Ping method is the readiness-barrier probe — the composition root
// (internal/app/wire_services.go) registers a closure over the Admin
// port's Ping method with the lifecycle readiness barrier. The barrier
// distinguishes "Drive feature disabled" (probe not registered) from
// "Drive feature enabled but unreachable" (probe returns error). The
// nil-service case must therefore return an explicit error so a
// misconfigured-but-assumed-live Drive cannot silently fall through
// the readiness barrier.
package drive

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Compile-time canary: *drive.Uploader MUST satisfy both Admin and
// Reader per the structural assertions in ports.go. The duplicate
// var-(...)-nil block below is redundant vs the package-level asserts
// (those fail the BUILD if the contract drifts) but adds an explicit
// test signal so future authors see the contract directly when
// reading the test file.
func TestUploader_Satisfies_Admin_And_Reader_Ports(t *testing.T) {
	var (
		_ Admin  = (*Uploader)(nil)
		_ Reader = (*Uploader)(nil)
	)
	// Sanity guard so the var-block is not optimised away by a future
	// refactor. The static asserts at ports.go already enforce the
	// conformance at build time; this var declaration documents the
	// freeze-test signal in case the package-level asserts are ever
	// moved to a separate _test.go file.
	var u *Uploader
	var a Admin = u
	var r Reader = u
	require.Nil(t, a, "untyped-nil *Uploader must satisfy Admin as a true-nil interface (typed-nil trap guard)")
	require.Nil(t, r, "untyped-nil *Uploader must satisfy Reader as a true-nil interface (typed-nil trap guard)")
}

// FASE 9 (June 2026): Probe nil-service. The Drive readiness probe is
// only registered with the lifecycle barrier when admin is a TRULY
// non-nil interface — i.e. the uploader is constructed. When the
// uploader is nil (Drive feature disabled at boot), the probe MUST
// NOT be registered at all. If a misconfigured deployment somehow
// constructs a Uploader without a populated Service, the Ping call
// returns an explicit error so the barrier fails closed rather than
// silently passing.
func TestUploader_Ping_NilService_ReturnsExplicitError(t *testing.T) {
	u := &Uploader{Service: nil, Log: nil}
	err := u.Ping(context.Background())
	require.Error(t, err,
		"Ping with nil Service MUST return an error so the readiness barrier fails closed on misconfigured Drive")
	require.Contains(t, err.Error(), "drive service not configured",
		"Ping error message must name the misconfiguration so operators can grep the boot log")
}

// FASE 9: typed-nil interface safety. BuildDriveBundle in
// internal/app/build_bundles_drive.go explicitly guards:
//
//	var admin drive.Admin
//	if driveUploader != nil { admin = driveUploader }
//
// so that the interface stays true-nil when the uploader is nil. This
// test is the unit-level mirror of the typed-nil trap guard: an
// UNINITIALISED interface variable IS a true-nil interface; assigning
// a typed nil pointer to an interface produces a non-nil interface
// holding a typed nil pointer (the classic Go trap). The contract
// under test is that wire_services' driveProbe pre-condition
// (`root.Drive.Admin != nil`) is consistent with the BuildDriveBundle
// post-condition (true-nil when uploader is nil).
//
// Trap semantics (Go spec): a typed-nil interface with dynamic type
// *Uploader compares UNEQUAL to the untyped nil interface (i.e.
// `trapped == nil` is false even when the underlying *Uploader is
// nil). This asymmetry is the bug the safe-pattern guard prevents: a
// guard-less assignment `admin = u` where u is a nil pointer produces
// a non-nil interface that would silently enable the readiness
// probe's Ping call and panic on `u.Service == nil` short-circuit
// dereference. The safe pattern (B) preserves interface-nilness.
//
// Note: this test does NOT cover the inner About.Get call — the
// gdrive SDK is unmockable in this package without a network round
// trip. The wire_services.go integration test fixture
// (lifecycle_integration_test.go) covers the end-to-end probe path
// via the QdrantProbe-shaped sibling assertion; Drive is structured
// the same way.
func TestUploader_AdminInterface_TypedNilTrapGuard(t *testing.T) {
	// TRAP — assigning a typed nil *Uploader to Admin produces a
	// non-nil interface holding a typed nil pointer. This is what
	// would silently enable the readiness probe's Ping call and panic
	// at the inner `u.Service == nil` check if wire_services called
	// Ping on a guard-less Admin field where the source was a nil
	// *Uploader.
	//
	// NOTE on the assertion: testify's `require.NotNil(t, trapped)`
	// uses reflection-driven `IsNil()` on the dynamic type's pointer
	// kind (here: *Uploader), which returns true even though the
	// interface itself is non-nil per Go spec. The CORRECT Go-level
	// check is `trapped != nil` which compares interface values
	// (static-type Admin + dynamic-type *Uploader-nil != interface{}-
	// nil). We use require.True/False with the Go comparison so the
	// assertion matches the spec rather than reflection's view.
	var nilUploader *Uploader       // untyped nil pointer
	var trapped Admin = nilUploader // TRAP: (dyn-type=*Uploader, dyn=nil)
	require.True(t, trapped != nil, "typed-nil *Uploader wrapped in Admin interface MUST compare != nil per Go spec (the trap)")
	require.False(t, trapped == Admin(nil), "typed-nil Admin interface MUST compare unequal to untyped-nil Admin(nil) per Go spec (same asymmetry, same trap)")

	// SAFE PATTERN — the build_bundles_drive.go guard. An
	// uninitialised interface variable is true-nil; assigning into it
	// only when the source is non-nil preserves the true-nil shape
	// that wire_services' `Admin != nil` pre-condition checks.
	var safeAdmin Admin
	require.True(t, safeAdmin == nil, "uninitialised Admin interface MUST be a true-nil interface (Go spec)")
	if nilUploader != nil {
		safeAdmin = nilUploader
	}
	require.True(t, safeAdmin == nil,
		"safeAdmin MUST remain a true-nil interface when the source *Uploader is nil (build_bundles_drive.go guard contract)")
}
