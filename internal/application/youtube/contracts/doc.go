// Package contracts declares the cross-boundary interfaces and envelope
// types for the youtube pipeline.
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): YouTube-specific declarative
// types only — no business logic, no orchestration, no external adapter
// imports.
//
// Import discipline:
//   - MUST NOT import other youtube/ sub-packages.
//   - MAY import internal/domain/asset (canonical asset types) and
//     external stdlib + leaf packages.
package contracts
