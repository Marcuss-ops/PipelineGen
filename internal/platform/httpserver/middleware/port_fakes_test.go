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
	"context"
	"crypto/sha256"
	"encoding/hex"

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
func (t *testFlags) ScriptClipsEnabled() bool { return t.scriptClips } // testM2MSecurity is a fake implementing middleware.M2MSecurityPort for
// the jobClientAuthMiddleware / requireScope tests. HashClientSecret
// mirrors the production SHA-256 SSOT so the digest round-trips through
// LookupClient exactly as it does in the real SQLite store.
type testM2MSecurity struct {
	enabled bool
	// clients maps secret_hash -> *M2MClient. The test populates it via
	// HashClientSecret so the digest shape matches LookupClient's probe.
	clients map[string]*middleware.M2MClient
	// lookupErr, when non-nil, makes LookupClient return (nil, err) so the
	// 500 store-unavailable path is testable without a real DB.
	lookupErr error
}

func (t *testM2MSecurity) EnableM2M() bool { return t.enabled }

func (t *testM2MSecurity) HashClientSecret(plaintext string) string {
	// Mirror the production canonical SSOT (kernel/digest.SHA256String)
	// without importing kernel/digest here — the test fake stays leaf-
	// clean. A drift between this and the real adapter would surface as a
	// test failure (LookupClient never finds the row).
	return sha256Hex(plaintext)
}

func (t *testM2MSecurity) LookupClient(_ context.Context, secretHash string) (*middleware.M2MClient, error) {
	if t.lookupErr != nil {
		return nil, t.lookupErr
	}
	return t.clients[secretHash], nil
}

// sha256Hex is the test-local mirror of digest.SHA256String. Kept here so
// the test fake does not pull kernel/digest into the middleware test
// package (which is leaf-clean per PG-006).
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Compile-time fakes satisfy the canonical ports.
var (
	_ middleware.AuthSecurityPort = (*testSecurity)(nil)
	_ middleware.FeatureFlagsPort = (*testFlags)(nil)
	_ middleware.M2MSecurityPort  = (*testM2MSecurity)(nil)
)
