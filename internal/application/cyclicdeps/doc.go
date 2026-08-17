// Package cyclicdeps is the canonical home for the
// "no import cycles in the internal package graph" regression-guard.
//
// Two guards live here, both executing the official toolchain command
// `go list -deps -e -json` to evaluate the package graph metadata with
// the same accuracy as the compiler itself (respecting //go:build tags,
// OS/Arch constraints, module-mode, etc.):
//
//   - TestNoImportCyclesInApplicationLayer — the application layer
//     (internal/application/...) must have ZERO import cycles.
//   - TestNoImportCyclesInInternalTree — the whole internal tree
//     (internal/...) must have ZERO import cycles, so every package
//     can be built and tested in isolation.
//
// This package ships as pure TDD test surface (no production symbols)
// so it has zero composition-root wiring cost. The hermetic detector
// requires only the Go toolchain (already required for the project) —
// no guru / goda dependency.
package cyclicdeps
