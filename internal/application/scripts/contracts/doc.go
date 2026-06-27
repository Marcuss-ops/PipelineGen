// Package contracts declares the cross-boundary interfaces and envelope
// types for the scripts pipeline.
//
// PR-G (PipelineGen Wave 22, June 2026, ADR-0002 §D4): this package is
// the canonical home for declarative types ONLY — no business logic,
// no orchestration, no external adapter imports.
//
// Import discipline:
//   - MUST NOT import other scripts/ sub-packages.
//   - MAY import internal/domain/* (canonical domain types — e.g.
//     internal/domain/script) and external stdlib + leaf packages
//     (pkg/textutil, pkg/sliceutil, etc.).
//   - No upstream dependency on adapters, usecase, or infrastructure.
//
// Sub-packages (usecase, jobs, ports, adapters, dto, events) MAY
// import this package. The reverse is forbidden by Go's import-cycle
// detector and godlike/07 §"Cross-file redeclaration within one
// Go package" forbids the wire-mirror pattern this discipline
// prevents.
package contracts
