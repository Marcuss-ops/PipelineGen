// Package persistence — voiceover's application-layer repository
// port (P1-2 boundary split, June 2026).
//
// Single responsibility: declare the narrow Repository interface +
// the canonical VoiceoverRecord wire-shape that voiceover uses to
// read/write/dedupe the voiceovers SQLite row lifecycle.
//
// Why a sub-package instead of leaving the interface in ports.go:
//   - Stable location for the persistence boundary. Future
//     PR-VO-* work that adds new repository methods (e.g. a
//     recently-deprecated dedupe-by-folder-id helper) lands
//     here without touching voiceover/ports.go's other 6 ports.
//   - No import cycle: this package does NOT import the parent
//     voiceover package. The parent imports THIS package for the
//     Record + Repository types (Service struct fields,
//     usecase.go method receivers). AGENTS.md Pattern 8 — only
//     ports for the application layer.
//
// Concrete adapter ownership: the production implementation
// wrapping *sqassets.VoiceoversRepository lives in
// internal/app/adapters_voiceover_use_case.go (file:
// adapters_voiceover_use_case.go::useCaseRepoAdapter). The
// adapter struct on the right side declares the compile-time
// assertion `var _ persistence.Repository = (*useCaseRepoAdapter)(nil)`
// so drift between the adapter signature and the port contract
// surfaces as a build error here, NOT at the voiceover call site
// (Pattern 0 — port abstraction layer, AGENTS.md).
package persistence
