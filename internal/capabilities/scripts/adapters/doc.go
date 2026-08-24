// Package adapters wires the canonical external service adapters
// (ollama.Generator, image/voiceover services)
// to the scripts pipeline. These are the concrete infrastructure
// references — pass them via interfaces in contracts/ports/ to keep
// application-layer code testable.
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): engine.go's ollama + memory
// adapters move here; engine.go's pure orchestration moves to usecase/.
//
// Import discipline:
//   - MAY import contracts/, dto/, ports/, usecase/.
//   - MAY import external infrastructure (infrastructure/ai/ollama,
//     compatibility-shims, etc.) under godlike/06 §"Single capability
//     ownership" strictness.
//
// Performs no I/O orchestration; each adapter is a thin wrapper.
package adapters
