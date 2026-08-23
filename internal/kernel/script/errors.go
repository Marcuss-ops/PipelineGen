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
