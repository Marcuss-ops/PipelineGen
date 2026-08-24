// Package usecase orchestrates the canonical youtube pipeline: the
// Service orchestrator + the search capability + the extraction
// capability + the metadata capability + the segments capability +
// the tagutil capability. Implements AGENTS.md Pattern 7.
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): existing youtube/ domain
// sub-packages (extraction/, metadata/, segments/, tagutil/, search/)
// are MELTED into this 7-way taxonomy. The youtube.Service struct
// (was at service.go root) moves here to usecase/service.go.
//
// Import discipline:
//   - MAY import contracts/, ports/, dto/, jobs/ (peer dependencies).
//   - MUST NOT import events/ (events emitted upward).
package usecase
