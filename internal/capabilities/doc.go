// Package capabilities is the parent of the vertical business capability
// slices declared in docs/architecture/godlike/02_TARGET_STRUCTURE.md
// §"internal/capabilities". Each child directory owns exactly one
// business capability and exposes the standard layout (module.go +
// contract.go + service.go + http.go + jobs.go + events.go +
// repository.go + adapters.go + *_test.go) per §"internal/capabilities".
//
// EXPAND-phase placeholder (Wave 19, June 2026). Production capability
// code authoritatively lives in internal/application/<x>/ until the
// per-capability BACKFILL/CUTOVER waves relocate it under this root. Per
// docs/architecture/godlike/05_DEPENDENCY_RULES.md, a capability depends
// only on the kernel + platform; cross-capability imports are forbidden.
//
// Forbidden top-level utility-style names under this root: service,
// repository, models, utils, helpers, common (per policy.yaml
// forbidden_top_level_dirs and 02_TARGET_STRUCTURE.md §"internal/capabilities").
//
// Do not import this package from production code in Wave 19.
package capabilities
