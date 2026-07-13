// Package errors is one of the kernel subzones declared in
// docs/architecture/godlike/02_TARGET_STRUCTURE.md §"internal/kernel".
// It is reserved for canonical errors and wrapping conventions shared by
// at least two capabilities (ErrNotFound, ErrInvalidPayload, ErrTerminal,
// format-stable error sentinels referenced from API responses).
//
// EXPAND-phase placeholder (Wave 19, June 2026). Production packages
// continue to declare their own error sentinels locally until a backfill
// collects them under a single canonical signature, per the
// "≥2 real capabilities need the same semantic contract" rule.
package errors
