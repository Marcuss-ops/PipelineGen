// Package ports holds the application-layer port interfaces that
// the scripts package consumes (e.g. Broker, JobEnqueuer). Sentinel
// errors + var _ <Port> = (*<Concrete>)(nil) compile-time assertions
// also live here per AGENTS.md Pattern 0 + fail-closed contract from
// youtube.ports precedent.
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): the canonical ports.go
// content (Broker, JobEnqueuer) moves here from scripts/ root so
// port declarations colocate with the related sentinel errors.
//
// Import discipline:
//   - MUST NOT import other scripts/ sub-packages (ports are the
//     seam — circular deps would defeat the abstraction).
//   - MAY import internal/domain/* and stdlib-only.
//   - Internal infrastructure implementations satisfy these ports
//     via implicit-interface patterns, never via direct import.
package ports
