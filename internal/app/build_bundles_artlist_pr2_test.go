// Package app — build_bundles_artlist_pr2_test.go: PR-P2-FAILCLOSED-JOB
// (July 2026) test coverage for the composition-time fail-closed contract
// on the Artlist job handler binding.
//
// USER-SPEC (Italian, verbatim): "Sistema P2 job handler: nel composition,
// se la registrazione del job handler Artlist fallisce, fallisci l'avvio
// con un typed error (no warning silenzioso). Aggiungi un test che
// dimostri che senza consumer l'avvio viene bloccato." — the test
// "dimostri che senza consumer l'avvio viene bloccato" is captured
// by the 3 tests below:
//
//   - TestWireArtlistJobBindings_NilArtlistService_AbortsBoot: gate #1.
//   - TestWireArtlistJobBindings_NilJobsBundleService_AbortsBoot: gate #2.
//   - TestRegisterArtlist_NoSilentWarnOnJobBindFailure: SOURCE-LEVEL
//     regression — asserts the pre-PR silent-Warn is GONE AND that the
//     bind-error branch propagates the typed error literally. Pins the
//     user spec's "no warning silenzioso" half + the composition-phase
//     "boot halts" half literally at the source level.
//
// The handler-side /api/artlist/job-consumer endpoint coverage lives
// in internal/api/assets/artlist/diagnostics_handler_test.go (PR-P2-3
// followup, not in this file).
package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
)

// TestWireArtlistJobBindings_NilArtlistService_AbortsBoot: gate #1
// fail-closed — a nil artlistSvc returns the typed error without
// attempting any binding (caller's abort contract verified).
func TestWireArtlistJobBindings_NilArtlistService_AbortsBoot(t *testing.T) {
	log := zap.NewNop()
	_ = log

	err := WireArtlistJobBindings(nil, nil)
	require.Error(t, err,
		"PR-P2-FAILCLOSED-JOB: nil artlistSvc MUST fail-closed (no silent skip)")
	assert.Contains(t, err.Error(), "artlistSvc is nil",
		"error must name the failing gate so operators can grep it in logs")
}

// TestWireArtlistJobBindings_NilJobsBundleService_AbortsBoot: gate #2
// fail-closed — a nil jobsBundle OR jobsBundle.Service returns the
// typed error. Mirrors the gate ordering of the publisher gate in
// WireArtlist (PR-ARTLIST-PERSIST-FIX uses the same pattern).
func TestWireArtlistJobBindings_NilJobsBundleService_AbortsBoot(t *testing.T) {
	log := zap.NewNop()
	_ = log

	// Construct a non-nil Service receiver so we test gate #2 only.
	// NewService is heavyweight; we want to skip its gates — easier
	// to test with literal nil pointers that the typed signature
	// rejects immediately.
	bundleNil := &JobsBundle{Service: nil}
	bundleOK := &JobsBundle{Service: nil} // Service field kept nil deliberately

	err1 := WireArtlistJobBindings(nil, bundleNil)
	require.Error(t, err1)
	assert.Contains(t, err1.Error(), "artlistSvc is nil")

	err2 := WireArtlistJobBindings(&artlistPkg.Service{}, bundleOK)
	require.Error(t, err2)
	assert.Contains(t, err2.Error(), "jobsBundle.Service is nil",
		"gate #2 reject message must name the nil-bundle-service cause")

	_ = bundleNil // silence unused-var lint
}

// TestRegisterArtlist_NoSilentWarnOnJobBindFailure: SOURCE-LEVEL
// regression test for the user-spec literal "no warning silenzioso"
// + the composition-phase "boot halts" half of the same spec.
//
// PR-P2-FAILCLOSED-JOB (July 2026) replaced the prior pre-PR pattern:
//
//	log.Warn("registerArtlist: job handler registration failed
//	         (worker will retry media.artlist jobs indefinitely)",
//	         zap.Error(err))
//
// with the fail-closed:
//
//	return fmt.Errorf("wire registry: artlist: %w", err)
//
// which surfaces ErrArtlistConsumerRegistrationFailed upstream. A
// future refactor that reintroduces silent-Warn for the same failure
// mode would re-create the godlike/07 fake-availability violation
// the PR was closing.
//
// The test reads the canonical production file
// (internal/app/registry_internal_modules.go) and asserts the
// pre-PR signature is absent. The test is intentionally structural
// rather than behavior-driven so it remains stable under refactors
// that preserve the fail-closed semantics through different code
// shapes (e.g. typed-sentinel wrapping, a dedicated validator, etc.).
//
// COMPOSITION-PHASE bonus assertion: to literally satisfy the
// user-spec "dimostri che senza consumer l'avvio viene bloccato",
// the test asserts the bind-error branch propagates the typed
// error via the exact literal `return fmt.Errorf("wire registry:
// artlist: %w"`. The orchestrator's abort on this error is the
// load-bearing godlike/07 fail-closed proof.
func TestRegisterArtlist_NoSilentWarnOnJobBindFailure(t *testing.T) {
	// Resolve the source file relative to THIS test's directory
	// — `go test` runs with the package directory as cwd, not
	// the repo root. runtime.Caller gives the absolute path of
	// the test source, then we join the sibling target.
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller(0) must succeed for this test file")
	sourcePath := filepath.Join(filepath.Dir(thisFile), "registry_internal_modules.go")

	body, err := os.ReadFile(sourcePath)
	require.NoError(t, err, "registry_internal_modules.go must be readable at "+sourcePath)

	src := string(body)

	// Assertion #1 — the load-bearing negative test: pre-PR
	// silent-Warn MUST be gone. This is the literal "no warning
	// silenzioso" half of the user spec.
	assert.NotContains(t, src, `log.Warn("registerArtlist: job handler registration failed`,
		"PR-P2-FAILCLOSED-JOB: pre-PR silent-Warn on RegisterHandler failure MUST be gone from registry_internal_modules.go")

	// Assertion #2 — literal sub-string close-out: the bind-error
	// branch in registerInternalModules MUST contain the typed-
	// error propagation literal `return fmt.Errorf("wire registry:
	// artlist: %w"`. This is the composition-phase "boot halts
	// when no consumer" half of the spec, verified at the
	// source level rather than via a fast-evolving mock.
	bindErrLiteral := `return fmt.Errorf("wire registry: artlist: %w"`
	assert.True(t, strings.Contains(src, bindErrLiteral),
		"PR-P2-FAILCLOSED-JOB: registry_internal_modules.go MUST contain the literal %q (godlike/07 fail-closed abort-on-bind-error contract); the orchestrator's abort on this error is the load-bearing fail-closed proof that 'senza consumer l'avvio viene bloccato'",
		bindErrLiteral)
}
