// Package search — errors.go defines the canonical sentinel errors
// consumed by the Aggregator (aggregator.go) and handlers. Every
// sentinel uses errors.New so callers can probe with errors.Is.
//
// Fase 6 (July 2026): ErrSemanticBackendUnavailable,
// ErrNoEligibleBackends, ErrAllBackendsFailed, and
// ErrCursorEncoding are the four new fail-closed sentinels
// that replace the pre-Fase-6 silent-degrade paths.
//
// Pre-existing sentinels (in types.go): ErrInvalidCursor,
// ErrEmptyCandidate.
// Pre-existing sentinels (in ports.go): ErrAlreadyRegistered,
// ErrFrozen, ErrNilBackend, ErrEmptyName.
package search

import "errors"

var (
	// ErrSemanticBackendUnavailable is returned by the Aggregator
	// when Query.Mode == SearchModeHybrid but no semantic backend
	// is registered in the BackendRegistry. Handlers map this
	// to HTTP 422 (semantic error: the client requested a mode
	// that the current deployment does not support).
	//
	// Callers MUST NOT silently degrade hybrid → ANN on this
	// error. The client explicitly requested hybrid retrieval;
	// falling back to ANN would violate the contract.
	ErrSemanticBackendUnavailable = errors.New("search: semantic backend not available for hybrid mode — register a semanticSearchBackend or retry with mode=ann")

	// ErrNoEligibleBackends is returned by the Aggregator when
	// zero backends match the query after source + media-type
	// filtering. The previous behaviour (PR 8/9) returned an
	// empty Result with Partial=false — a silent success that
	// hid misconfiguration. Handlers map this to HTTP 422.
	ErrNoEligibleBackends = errors.New("search: no eligible backends for query — check sources and media_types filters")

	// ErrAllBackendsFailed is returned by the Aggregator when
	// every eligible backend returned an error. Unlike the
	// Partial case (some backends succeeded, some failed), this
	// means the search produced zero results from any source.
	// Handlers map this to HTTP 502 (bad gateway: all upstream
	// backends failed).
	ErrAllBackendsFailed = errors.New("search: all eligible backends failed — check ProviderErrors for per-backend diagnostics")

	// ErrCursorEncoding is returned when the Aggregator cannot
	// produce a NextCursor from the merged result set. This is
	// distinct from ErrInvalidCursor (malformed input cursor):
	// the input was valid, but the output cursor could not be
	// serialised (base64 or JSON encoding failure). Handlers
	// map this to HTTP 500 (internal error: cursor pipeline
	// broken).
	ErrCursorEncoding = errors.New("search: failed to encode next cursor — internal cursor pipeline error")
)
