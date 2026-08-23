// Package hashutil — typed-port for SHA-256-style string hashing
// (Commit D, July 2026).
//
// godlike/06 one-owner-per-fact: HashFunc is the SINGLE canonical port
// consumed by the domain-package idempotency-key derivation surface
// (internal/domain/remote/idempotency.go + complete_job_idempotency.go).
// The SHA-256 ALGORITHM is owned by internal/kernel/digest (SSOT); this
// package exposes it as the canonical HashFunc value, and infrastructure
// adapters may return it as such.
//
// Functional-type-as-single-method-interface idiom: any
// `func(string) string` value satisfies HashFunc without an explicit
// implements clause (Go's structural typing on function types). The
// unit test in hashutil_test.go declares `var _ hashutil.HashFunc =
// fakeHash` to lock the contract against future drift (e.g. someone
// turning the function-type into a method interface).
//
// Naming: the package path `internal/domain/remote/hashutil` matches
// the user spec literal `internal/domain/remote/hashutil.HashFunc`.
// The package name is `hashutil`; qualified as `hashutil.HashFunc`.
// Infrastructure adapters depend on this package; this package never
// depends on a concrete storage, transport, or filesystem implementation.
package hashutil

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// SHA256String is the canonical HashFunc implementation: it delegates the
// algorithm to the kernel digest SSOT (internal/kernel/digest), keeping the
// byte-identical output the port contract promises. It lives beside the port
// so legacy domain helpers can preserve their existing free-function API
// without importing an infrastructure adapter. New callers should prefer
// injecting a HashFunc into the Make* constructors.
func SHA256String(s string) string {
	return digest.SHA256String(s)
}

// HashFunc is the canonical typed-port for SHA-256-style string hashing.
//
// Signature: `func(s string) string` — the same input/output shape and
// byte-stable hex digest semantics used by infrastructure adapters.
//
// godlike/07 fail-closed contract: callers MUST inject a non-nil
// HashFunc at construction time. Nil HashFunc always panics in the
// canonical constructor wrappers (MakeArtifactIdempotencyKey,
// MakeCompleteJobIdempotencyKey) per the typed-port "explicit dep" pattern
// enforced across the codebase via the panic-on-missing-dep convention.
//
// Test surface: hashutil_test.go declares `var _ HashFunc = fakeHash` so
// the byte-fake contract is exercised at compile time. The unit tests
// for MakeArtifactIdempotencyKey + MakeCompleteJobIdempotencyKey (in
// idempotency_factory_test.go) exercise the runtime contract with a
// `func(s string) string { return "FAKE:" + s }` value.
type HashFunc func(s string) string
