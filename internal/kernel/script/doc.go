// Package script is one of the kernel subzones declared in
// docs/architecture/godlike/02_TARGET_STRUCTURE.md §"internal/kernel".
// It is reserved for stable script-shape primitives shared by the scripts
// capability, its consumers, and the jobs that carry scripts as payloads
// (Plan, GenerationSpec, payload codec identity, request/DTO markers).
//
// BACKFILL complete (Aug 2026): the production content that previously
// lived in internal/kernel/script/ was migrated here atomically (task 1:
// kernel/script → kernel/script). The legacy root is deleted; all importers
// consume this package directly.
// No transport, no SQL, no logger dependencies allowed here.
package script
