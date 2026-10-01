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

import "errors"

// Typed errors returned by the script generation pipeline.
var (
	// ErrValidation means the GenerationSpec failed validation
	// (e.g., no topic and no clips provided).
	ErrValidation = errors.New("scriptjobs: validation failed")

	// ErrUnavailable means a required dependency (jobs service,
	// script writer, etc.) is not initialized.
	ErrUnavailable = errors.New("scriptjobs: service unavailable")

	// ErrConflict means a duplicate or conflicting request was
	// detected (e.g., same fingerprint already generating).
	ErrConflict = errors.New("scriptjobs: conflict")

	// ErrUnsupportedVersion means the payload version is not
	// recognized by this worker.
	ErrUnsupportedVersion = errors.New("scriptjobs: unsupported payload version")

	// ErrInvalidPayload means the payload is empty, not valid
	// JSON, or contains neither text nor clips.
	ErrInvalidPayload = errors.New("scriptjobs: invalid or empty payload")
)
