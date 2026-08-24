// Package jobs — registry_definitions.go (slim orchestrator, PR-SPLIT-JOBS-REGISTRY-DEFINITIONS, July 2026).
//
// 3-file split layout (per d44e0239 pkg/retry canonical pattern):
//
//	registry_definitions.go  (this file, slim orchestrator: package doc + canonical policy constants)
//	registry_timeout.go     (HC-1 typed port surface: TimeoutMap + TimeoutResolver)
//	registry_types.go       (RegistryEntry + JobPolicy + Type... const block)
//
// godlike/06 SSOT (one-canonical-owner-per-fact): this file is the
// canonical SOLE owner of:
//
//   - The Wave 19 / P1-9 canonical policy constants (DefaultQueue
//   - DefaultConcurrency) — the single source of truth for
//     default routing / concurrency values. The typed accessors
//   - Compose()'s applyDefaults pass both reference these
//     constants so a future rename (e.g. "primary" instead of
//     "default") is a one-line change.
//   - The 3-file-split breadcrumb (per d44e0239 precedent) so
//     future readers land on the right file when looking up
//     canonical policy defaults.
//
// Lookup paths preserved: jobs.DefaultQueue, jobs.DefaultConcurrency
// resolve identically pre/post split (same package).
package policy

// ── Wave 19 / P1-9 canonical policy constants (June 2026) ─────────────────
//
// Single source of truth for default routing / concurrency values; the
// typed accessors + Compose()'s applyDefaults pass both reference these
// constants so a future rename (e.g. "primary" instead of "default") is
// a one-line change. Co-located with the 3-file-split breadcrumb above
// for godlike/06 SSOT — the policy record + its canonical defaults live
// together in this slim orchestrator.
const (
	// DefaultQueue is the canonical routing label assigned to any
	// registered job type whose Queue field is empty. The string is
	// hard-coded here so the typed accessor Queue(t), the applyDefaults
	// pass in Compose(), and any future operator-facing dashboard
	// surface agree on the same default label.
	DefaultQueue = "default"
	// DefaultConcurrency is the canonical per-worker parallel-lease
	// budget for any registered job type whose Concurrency field is
	// zero or negative. The value 1 mirrors the pre-Wave-19 in-process
	// broker semantics: a worker polls one job at a time.
	DefaultConcurrency = 1
)
