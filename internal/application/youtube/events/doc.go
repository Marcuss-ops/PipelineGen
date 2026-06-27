// Package events declares the typed event payloads the youtube
// pipeline emits to external observers (channel-monitor, jobs
// observers, metrics, http SSE streams). Events are emitted from
// usecase/ upward; listeners MUST NOT feed events back into
// usecase/ (cycle break).
//
// PR-G (Wave 22, June 2026, ADR-0002 §D4): reserved for future
// event surface. Pre-PR-G the pipeline emitted via inline log
// lines and progress.update. Post-PR-G typed events become the
// canonical contract.
//
// Import discipline:
//   - MUST NOT import other youtube/ sub-packages.
//   - MAY import contracts/, dto/ (event payloads and shared value types).
package events
