// Package generation — response.go.
//
// Wave 0 of audit 2026-07-03 P0 #3 (Single canonical scripting framework)
// promoted the canonical envelope surface (Mode / ModeSync / ModeAsync /
// Response[T] / Sync[T] / Async[T]) to internal/application/scripts/apiutil.
// This file now carries TYPE-ONLY retargeting aliases (`=` Go type aliases)
// so that legacy code paths and any third-party consumer reading
// generation.Response[T] / generation.Mode / generation.ModeSync /
// generation.ModeAsync remain byte-identical to the canonical apiutil
// surface, with zero behavioural change.
//
// What this file does now:
//
//   - type Response[T any] = apiutil.Response[T] — alias, byte-identical.
//   - type Mode = apiutil.Mode — alias, byte-identical.
//   - const ModeSync = apiutil.ModeSync, const ModeAsync = apiutil.ModeAsync
//     — byte-identical constants via compile-time substitution.
//   - func Sync[T any](kind, result) Response[T] — thin wrapper that
//     delegates construction to the aliased apiutil.Response[T] struct.
//     CUTOVER wave (next) flags this with ErrDeprecatedWireEnvelope
//     side-effect log (godlike/07 no-fake-availability pattern) and the
//     audit-pin doc points to internal/application/scripts/apiutil as
//     the canonical home. CONTRACT wave (final) physically removes this
//     file via git-rm once the CUTOVER wave's deprecation window closes.
//
// Why this is safe (godlike/06 SSOT preservation):
//
//   - The canonical struct shape lives in internal/application/scripts/
//     apiutil/response.go; this file's type aliases resolve to that
//     location at compile time. There is no shadow struct.
//   - Fields accessed on a legacy `generation.Response[T]` value resolve
//     to the apiutil.Response[T] field set — JSON tags, omitempty rules,
//     package-qualifier identities all invariant.
//   - Mode comparisons through the legacy `generation.Mode` alias resolve
//     to apiutil.Mode (string-typed). External code that still does
//     `resp.Mode == generation.ModeSync` keeps working because both
//     sides of the comparison resolve to the same string-typed constant.
//
// Wave 1 BACKFILL migrates the 3 known callers (books/process,
// books/process_drive, lessons/generate) to consume apiutil directly.
// legacy `generation.Response` / `generation.Mode` continue to
// compile-via-alias so any non-envelope code (tests, mocks, transitive
// helpers) does not break.
package generation

import (
	apiutil "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/apiutil"
)

// ── Type aliases (godlike/06 SSOT pointer to apiutil) ────────────

// Response alias — byte-identical to apiutil.Response[T]. CUTOVER/CONTRACT
// will retire this alias in successive atomic commits.
type Response[T any] = apiutil.Response[T]

// Mode alias — byte-identical to apiutil.Mode. The legacy `Mode` type
// identifier continues to compile in this package via alias resolution.
type Mode = apiutil.Mode

// Mode constants — compile-time constant folding through the alias
// chain ensures byte-identity with apiutil.ModeSync / apiutil.ModeAsync.
// External code that compares to generation.ModeSync still resolves
// to the canonical string "sync" / "async" at compile time.
const (
	ModeSync  = apiutil.ModeSync
	ModeAsync = apiutil.ModeAsync
)

// ── Function constructors (canonical delegation pattern) ────────
//
// These functions delegate to the aliased apiutil.Response[T] struct
// shape. The constructor logic itself is unchanged from pre-Wave-1
// because the target struct semantics are byte-identical. CUTOVER
// wave will fail-closed these with ErrDeprecatedWireEnvelope; until
// then they remain operational and the audit-pin comment above
// signals the deprecation posture to operators and downstream
// consumers.

// Sync constructs a successful synchronous Response[T] envelope by
// delegating the field-fill semantics to the aliased apiutil.Response[T].
func Sync[T any](kind string, result T) Response[T] {
	return Response[T]{
		OK:     true,
		Kind:   kind,
		Mode:   ModeSync,
		Result: &result,
	}
}

// Async constructs a successful async-acknowledgment Response[T] envelope
// by delegating the field-fill semantics to the aliased apiutil.Response[T].
func Async[T any](kind, jobID, status, jobType string) Response[T] {
	return Response[T]{
		OK:      true,
		Kind:    kind,
		Mode:    ModeAsync,
		JobID:   jobID,
		Status:  status,
		JobType: jobType,
	}
}
