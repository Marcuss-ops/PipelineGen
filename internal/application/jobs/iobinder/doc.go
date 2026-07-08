// Package iobinder provides the forward-prevention regression-guard for
// PR-REFACTOR-P2-BLOCKING-IO from
// architecture/action-plans/2026-08-08-refactor-checklist-action-plan.md.
//
// # Spec (literal, P2, deadline 2026-08-20)
//
//	Remove synchronous os.ReadFile / os.Open in hot paths; lift to
//	eager-load at boot via injected I/O binder. Verification:
//	`rg 'os\.ReadFile|os\.Open' internal/application/jobs/` audited +
//	each call site has a benchmark showing improvement.
//
// # Current state on origin/main (canonical baseline at audit time)
//
// os.ReadFile hits: 0
// os.Open hits: 1 — internal/application/jobs/assets/service.go:83
//
//	(inside Service.Download — per-asset file open; NOT cacheable at
//	boot, NOT in the spec's "lift to eager-load" scope; the file path
//	is dynamic per assetID and only known at request time).
//
// The 1 os.Open hit is documented in the test's exceptionList as the
// canonical baseline. When a future agent migrates the per-asset
// download path to a typed I/O binder (port-based DI), they remove the
// entry from exceptionList AND ship a benchmark proving the migration.
//
// # Out of scope (NOT in the spec's verification pattern)
//
// The following sync I/O patterns exist in internal/application/jobs/
// but are NOT covered by `os.ReadFile|os.Open` and are out of scope for
// this PR:
//   - 2 os.Stat calls (internal/application/jobs/worker/runner_upload.go:128, 160)
//     — per-artifact existence checks at upload time; NOT cacheable.
//   - 2 os.Create calls (internal/application/jobs/assets/service.go:160,
//     internal/application/jobs/worker/tools.go:196)
//     — write targets at upload time; NOT cacheable.
//
// These are tracked separately under PR-IOBINDER-P2-BROADER (forward-pointer,
// deadline TBD) which widens the spec's verification pattern.
//
// # Sub-PRs that will shrink the exception list to zero
//
//	PR-IOBINDER-P2-DOWNLOAD: migrate Service.Download's per-asset os.Open to
//		typed port (internal/infrastructure/localasset); remove service.go:83
//		from exceptionList + ship benchmark proving the improvement.
package iobinder
