// Package events declares the typed event payloads the scripts
// pipeline emits to external observers (job observers, metrics,
// http SSE streams). Events are emitted from usecase/ upward;
// listeners MUST NOT feed events back into usecase/ (cycle break).
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): reserved for future
// event surface — pre-PR-G the pipeline emitted via inline log
// lines and progress.update. Post-PR-G typed events become the
// canonical contract.
//
// Import discipline:
//   - MUST NOT import other scripts/ sub-packages.
//   - MAY import contracts/, dto/ (event payloads and shared
//     value types).
//   - Event listeners (api/, monitor/) MAY import this.
package events
