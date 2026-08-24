// Package jobs — sqlite-backed job store (formerly sentinels).
//
// Wave 17.1.2 (June 2026) removed the legacy Store interface; the
// contract now lives in internal/domain/job.Store and *SQLiteStore
// is the only in-tree implementation.
//
// P0.F regression-surface synergy (July 2026): the typed sentinels
// `ErrLeaseLost`, `ErrTransitionConflict`, and `ErrJobNotFound` that
// used to live at store.go:14,18 are now re-exported as godlike/06
// SSOT-compliant aliases in `repository_commands.go` (same package,
// top-of-file `var (...)` block). Phase A.1 cutover (June 2026) moved
// the canonical decls to `internal/domain/job/errors.go`; Phase A.2
// (July 2026) re-establishes the sqlite-side aliases so internal
// callers (lifecycle_*.go, finalize_attempt.go) can continue
// referencing the names unprefixed without re-declaring them as
// `errors.New(...)` (which would create identity drift and break
// `errors.Is` probes that cross-cut both import paths).
//
// Identity was preserved across the cutover: `errors.Is(err, jobs.ErrLeaseLost)`
// (the in-package alias at repository_commands.go) and
// `errors.Is(err, kerneljob.ErrLeaseLost)` (the canonical decl at
// internal/domain/job/errors.go) probe the SAME sentinel (same `error`
// value). External callers that previously imported
// `sqljobs.ErrLeaseLost` from this package can switch to either:
//   - `kerneljob.ErrLeaseLost` (canonical, recommended for cross-package callers)
//   - `jobs.ErrLeaseLost` (in-package, equivalent identity)
//
// `ErrFinalizeAttempt*` sentinels are re-exported as canonical aliases
// at `finalize_attempt.go` (canonical impl-site aliases per
// finalize_attempt.go::64-71). The local aliases are split between
// `repository_commands.go` (general-purpose ErrLeaseLost /
// ErrTransitionConflict / ErrJobNotFound / ErrInvalidState) and
// `finalize_attempt.go` (impl-site-specific FinalizeAttempt').
//
// This file is intentionally empty of `var ()` declarations — the
// canonical SSOT surface is owned by `repository_commands.go` and
// `finalize_attempt.go`. New typed sentinels MUST be added at one of
// those two files (not here) to keep the lockstep invariant with
// `internal/domain/job/errors.go`.
package jobs
