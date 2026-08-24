// Test-port stubs (PG-006, June 2026).
//
// The PG-006 typed-port sweep removed `internal/platform/config`
// from every middleware file. Tests that previously constructed a
// `&config.Config{Security: config.SecurityConfig{...}}` literal to
// drive the auth/ratelimit/featureflag middlewares now build a small
// fake implementing the corresponding port. These fakes stay local
// to the test package so the production api/middleware build tree
// keeps zero infra dependencies.
package middleware

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
)

// testSecurity is a 3-method fake implementing middleware.AuthSecurityPort.
// Constructed inline by the auth tests; the (admin, worker) token values
// are byte-exact compared via the production compareTokens helper so
// the tests still pin the constant-time + empty-misconfig invariants.
type testSecurity struct {
	enabled bool
	admin   string
	worker  string
}

func (t *testSecurity) EnableAuth() bool    { return t.enabled }
func (t *testSecurity) AdminToken() string  { return t.admin }
func (t *testSecurity) WorkerToken() string { return t.worker }

// testFlags is a 2-method fake implementing middleware.FeatureFlagsPort.
// Constructed inline by the feature-flag middleware tests.
type testFlags struct {
	artlist     bool
	scriptClips bool
}

func (t *testFlags) ArtlistEnabled() bool     { return t.artlist }
func (t *testFlags) ScriptClipsEnabled() bool { return t.scriptClips }

// Compile-time fakes satisfy the canonical ports.
var (
	_ middleware.AuthSecurityPort = (*testSecurity)(nil)
	_ middleware.FeatureFlagsPort = (*testFlags)(nil)
)
