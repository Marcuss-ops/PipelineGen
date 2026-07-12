// Package hashutil — typed-port for SHA-256-style string hashing
// (Commit D, July 2026).
//
// godlike/06 one-owner-per-fact: HashFunc is the SINGLE canonical port
// consumed by the domain-package idempotency-key derivation surface
// (internal/domain/remote/idempotency.go + complete_job_idempotency.go).
// The concrete SHA-256 implementation lives in
// internal/infrastructure/files.NewSHA256Hasher, which returns the
// canonical `func(string) string` value typed as HashFunc.
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
// Note: this is a SEPARATE package from `internal/infrastructure/files`
// (which historically called its helper `file.HashFunc`-style methods,
// but the package name there is `files`, not `hashutil`, so no
// package-name collision).
package hashutil

// HashFunc is the canonical typed-port for SHA-256-style string hashing.
//
// Signature: `func(s string) string` — maps 1:1 to files.SHA256String
// at internal/infrastructure/files/hashutil.go (same input/output shape,
// same byte-stable hex digest semantics).
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
