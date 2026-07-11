// Package ports — Clock port (Fase 5(a), July 2026).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// clock abstraction consumed by application-layer use cases. The
// production concrete is `time.Now` (via `timeutil.SystemClock` or
// direct call); tests inject a `*timeutil.FakeClock` to drive
// deterministic leases, retry backoffs, and outbox event timestamps.
//
// godlike/07 minimum-blast-radius: the interface is intentionally
// narrow (one method + one optional helper). A richer surface
// (Tickers, Stopwatches, Wall-vs-Mono time) would over-design for
// the application use cases that currently call `time.Now()` inline.
package ports

import "time"

// Clock is the canonical narrow port for time access. Every
// application-layer use case that needs the current time MUST take a
// Clock via the constructor (not call `time.Now()` directly) so tests
// can substitute a FakeClock and drive deterministic time-dependent
// state transitions.
//
// Errors: never returns an error — `time.Time` is always well-defined.
// A future port clock that wraps an injected clock-source with
// load-failure semantics would add an error return; the current
// surface is intentionally slimmer.
type Clock interface {
	// Now returns the current instant in the local timezone of the
	// implementation. Production concrete returns UTC-normalized
	// time (via `time.Now().UTC()`); Fixtures return whatever the
	// test set (typically UTC for cross-CI locale invariance).
	Now() time.Time
}

// SystemClock is the production concrete Clock. Wraps `time.Now()`
// with a UTC normalization at the seam so application code never
// has to call `.UTC()` site-by-site (Push 5.2 caller-migration will
// swap direct `time.Now()` calls for `clock.Now()` calls).
//
// godlike/07 fail-closed: SystemClock is the SOLE place in the
// codebase that calls `time.Now()`. Any new caller of `time.Now()`
// in the application layer is a godlike/07 violation tracked here.
type SystemClock struct{}

// Now returns `time.Now().UTC()`. UTC normalization at the seam
// matches the canonical store-layer RFC3339 encoding contract
// (timezone suffix 'Z' on every persisted timestamp).
func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}

// Compile-time identity lock (godlike/06 SSOT — concrete impl
// satisfies the Clock interface for drift-detection at build time).
var _ Clock = (*SystemClock)(nil)
