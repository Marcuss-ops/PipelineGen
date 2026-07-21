// Package mediamemory — helpers.go is the canonical SSOT for the
// skeleton-phase utilities shared across sibling files.
//
// godlike/06 SSOT (canonical error envelope for the skeleton
// phase): errNotImplemented is the SINGLE typed sentinel used by
// Phase 1.x method stubs (resolvers / rankers / services whose
// cognitive contract is declared but whose body is not yet
// implemented). Sibling files (resolver.go, ranker.go,
// binding_service.go, ...) import this helper verbatim.
//
// godlike/07 NO-FAKE-AVAILABILITY: the helper attaches the godlike/06
// canonical prefix to every Phase 1.x sentinel so callers can
// distinguish "method not implemented" from a typed business
// failure via errors.Is. Production callers MUST NOT consume
// errNotImplemented — they should treat it as a programmer error
// and stop (it is intentionally NOT a sentinel of the package's
// public API; codebase.archeck forward-pins it as a build gate).
package mediamemory

import "errors"

// errNotImplemented is the canonical helper for Phase 1.x stubs.
// It is package-private (lowercase) on purpose: callers outside
// the package MUST NOT depend on its surface.
//
// Pattern:
//
//	return zero, errNotImplemented("mediamemory: X.Y not yet implemented (Phase 1.x)")
//
// godlike/06 SSOT invariant: the message MUST include the package
// path (`mediamemory:`) so log scans correctly locate the offender.
// The optional `(Phase X)` suffix is required so the next phase can
// mechanically grep "Phase 1.x" occurrences to know what to fill.
func errNotImplemented(message string) error {
	return errors.New(message)
}
