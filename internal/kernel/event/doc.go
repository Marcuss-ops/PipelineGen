// Package event is one of the kernel subzones declared in
// docs/architecture/godlike/02_TARGET_STRUCTURE.md §"internal/kernel".
// It is reserved for canonical domain-event primitives (envelope shape,
// publish/subscribe contract, idempotency token) used by at least two
// capabilities for inter-slice messaging.
//
// EXPAND-phase placeholder (Wave 19, June 2026). Production content will
// arrive here once the SSOT registry work in
// docs/architecture/godlike/04_REGISTRIES_AND_SSOT.md produces a canonical
// envelope used by two real capabilities and the §"One owner per fact"
// rule is satisfied.
package event
