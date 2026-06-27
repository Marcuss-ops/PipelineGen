// Package jobs declares the youtube job handlers (clip extract,
// rebuild_search_text). The canonical jobs.Service registration
// is the only entry point.
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): the existing youtube/jobs/
// sub-package content migrates here — service.go (RebuildDeps +
// HandleRebuildSearchTextJob) + job types.
//
// Import discipline:
//   - MAY import contracts/, dto/, ports/.
//   - MUST NOT import usecase/ directly (deps injection at composition).
package jobs
