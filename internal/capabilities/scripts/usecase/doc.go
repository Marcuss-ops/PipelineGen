// Package usecase orchestrates the canonical script generation
// pipeline: engine + use cases + postprocessor registry + flow
// helpers. Implements the application-layer rules of thumb from
// AGENTS.md Pattern 7 (services — reuse existing canonical contract).
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): this package collects
// orchestration types previously scattered across the scripts/
// mega-package (engine.go, generate_*_usecase.go, postprocessor_*.go,
// processor_*.go, source_resolver_*.go, generation_*.go, etc.).
//
// Import discipline:
//   - MAY import contracts/, ports/, dto/, jobs/ (peer dependencies).
//   - MAY import adapters/ ONLY when the adapter is a typed port seam
//     (not a concrete infrastructure struct — use ports/ for that).
//   - MUST NOT import events/ (events are emitted upward; usecase
//     publishes them but does not consume them).
//
// Business logic lives here. No SQL, no HTTP, no shell-out — those
// concerns are routed through ports/adapters.
package usecase
