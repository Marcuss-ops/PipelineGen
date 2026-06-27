// Package jobs declares the script-generation job handlers and
// their typed payloads. Both async (enqueue + dispatch) and the
// in-process handler registration surface live here.
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): handlers previously
// inlined into scripts/'s generation_job.go / scriptflow_usecase.go
// migrate here so the canonical jobs.Service registration path is
// the only entry point.
//
// Import discipline:
//   - MAY import contracts/, dto/, ports/ (payload + port surfaces).
//   - MUST NOT import usecase/ directly — handlers construct
//     component pieces via deps injection at composition time.
//
// Handlers registered via the canonical jobs.Service.RegisterHandler
// API surface (PR-D Deps pattern et al).
package jobs
