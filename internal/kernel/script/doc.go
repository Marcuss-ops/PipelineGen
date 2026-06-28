// Package script is one of the kernel subzones declared in
// docs/architecture/godlike/02_TARGET_STRUCTURE.md §"internal/kernel".
// It is reserved for stable script-shape primitives shared by the scripts
// capability, its consumers, and the jobs that carry scripts as payloads
// (Plan, GenerationSpec, payload codec identity, request/DTO markers).
//
// EXPAND-phase placeholder (Wave 19, June 2026). Production content
// authoritatively lives in internal/domain/script/ until BACKFILL/CUTOVER.
// No transport, no SQL, no logger dependencies allowed here.
package script
