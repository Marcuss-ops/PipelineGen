package cas

import "errors"

// Typed error sentinels (godlike/07 fail-closed): every failure path in the
// CAS store wraps one of these via fmt.Errorf %w so callers can
// errors.Is-probe the class without parsing messages.
var (
	// ErrNotWired is returned when a method is invoked on a nil or
	// un-constructed store (composition error, not a runtime event).
	ErrNotWired = errors.New("cas: store not wired")

	// ErrInvalidConfig is returned by NewStore for a missing root.
	ErrInvalidConfig = errors.New("cas: invalid configuration")

	// ErrStagerRequired is returned by NewStore when Config.Stager is nil.
	// The store reuses the existing LocalStore (staging.Stager) as its
	// atomic write path; a nil stager is a wiring error, not a silent
	// fallback.
	ErrStagerRequired = errors.New("cas: Config.Stager is required (reuse the LocalStore)")

	// ErrInvalidSHA256 is returned when an address is not exactly 64
	// lowercase hex characters.
	ErrInvalidSHA256 = errors.New("cas: invalid sha256 address (want 64 lowercase hex chars)")

	// ErrObjectNotFound is returned by Open when the address has no object.
	ErrObjectNotFound = errors.New("cas: object not found")

	// ErrCorruption is returned when on-disk bytes differ from the address
	// (CAS_CORRUPTION_DETECTED semantics): either a Put landed different
	// bytes at an existing address, or a post-write verification failed.
	ErrCorruption = errors.New("cas: content corruption (on-disk bytes differ from the object address)")

	// ErrEmptyContent is returned by Put when the stager reports a zero-byte
	// object (empty content is not addressable — a 0-byte file still has a
	// valid digest, but the staging port rejects it by contract).
	ErrEmptyContent = errors.New("cas: refusing empty content")

	// ErrInvalidInput is the umbrella sentinel for bad call-site inputs
	// (nil reader).
	ErrInvalidInput = errors.New("cas: invalid input")
)
