// Package cyclicdeps is the canonical home for the
// "no P0 import cycles in the application layer" regression-guard.
//
// Per architecture/action-plans/2026-08-08-refactor-checklist-action-plan.md
// (PR-REFACTOR-P0-CYCLIC-DEPS, deadline 2026-08-12): the application
// layer (internal/application/...) must have ZERO import cycles. The
// canonical test surface is the file cyclicdeps_test.go which executes
// the official toolchain command
// `go list -deps -e -json ./internal/application/...` to evaluate the
// package graph metadata with the same accuracy as the compiler itself
// (respecting //go:build tags, OS/Arch constraints, module-mode, etc.).
//
// This package ships as pure TDD test surface (no production symbols)
// so it has zero composition-root wiring cost. The hermetic detector
// requires only the Go toolchain (already required for the project) —
// no guru / goda dependency.
package cyclicdeps
