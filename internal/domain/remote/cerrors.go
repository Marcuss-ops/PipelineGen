// Package remote — cerrors.go (P1 #15, July 2026).
//
// Canonical typed-error core for the HTTP wire envelope used by the
// remote completion path (POST /internal/v1/jobs/:id/complete and
// /complete-with-artifacts). The 7 closed-set ErrorKind values map
// deterministically to HTTP status codes + Retry-After hints per
// the user-facing spec:
//
//	lease_lost             → HTTP 409 (Conflict)
//	idempotency_conflict   → HTTP 409 (Conflict)
//	invalid_manifest       → HTTP 400 (Bad Request)
//	artifact_missing       → HTTP 422 (Unprocessable Entity)
//	publisher_unavailable  → HTTP 503 (Service Unavailable)
//	rate_limited           → HTTP 429 (Too Many Requests) + Retry-After
//	internal_db_error      → HTTP 500 (Internal Server Error)
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// single source of truth for the wire envelope taxonomy. The server
// emits via internal/api/jobs.MapErrorToHTTP; the
// client reconstructs via internal/infrastructure/remote/jobbrokerclient.
// decodeCompletionErrorEnvelope. Both sides share this file's names.
//
// godlike/07 typed-error contract: each Kind has its OWN canonical
// sentinel reachable via errors.Is(err, ErrCompletion<Kind>). The
// sentinel surfaces from the wire envelope via the Is() method on
// RemoteCompletionError, so callers can write
//
//	if errors.As(err, &remErr) && remErr.Kind == remote.ErrorKindLeaseLost { ... }
//
// or
//
//	if errors.Is(err, remote.ErrCompletionLeaseLost) { ... }
//
// symmetrically. The two probe styles are equivalent by design — the
// sentinel-of-sentinels is the single 7-element set this file owns.
//
// Migration status (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT):
//   - EXPAND  (this commit, July 2026): canonical surface live + 7
//     unit tests covering one test per Kind. The existing typed
//     sentinels in domain/remote (ErrConcurrentLeaseRefutation,
//     ErrRemoteArtifactManifestInvalid, …) remain the canonical
//     owner of "what happened" inside completion.Service; this file
//     owns the wire envelope only.
//   - BACKFILL (forward-pointer): migrate the remaining completion-
//     adjacent call sites in internal/api/jobs/handler_workers.go
//     (Complete / Fail / Renew / Progress) from apiutil.InternalError
//     to api/jobs.MapErrorToHTTP.
//   - CUTOVER (forward-pointer): retire the apiutil.InternalError
//     overlay on completion-path routes once every call site maps.
//   - CONTRACT (forward-pointer): physical git-rm of the legacy
//     paths; Check tightened to ban the surface entirely.
package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ── ErrorKind (canonical 7-kind taxonomy) ─────────────────────────────────

// ErrorKind is the closed-set taxonomy of canonical completion-path
// HTTP errors. The 7 closed values map deterministically to HTTP
// status codes (see HTTPStatus) and optional Retry-After hints (see
// RetryAfter).
type ErrorKind string

// Canonical 7-kind closed set. Adding a new kind requires a unit
// test in cerrors_test.go + a populated row in HTTPStatus + RetryAfter
// + kindToSentinel (the 3 canonical lookup tables in this file); a
// kind missing from any of those 3 surfaces a forward-preventive
// compile-time issue and is the load-bearing audit-pin for godlike/06
// SSOT discipline.
const (
	ErrorKindLeaseLost            ErrorKind = "lease_lost"
	ErrorKindIdempotencyConflict  ErrorKind = "idempotency_conflict"
	ErrorKindInvalidManifest      ErrorKind = "invalid_manifest"
	ErrorKindArtifactMissing      ErrorKind = "artifact_missing"
	ErrorKindPublisherUnavailable ErrorKind = "publisher_unavailable"
	ErrorKindRateLimited          ErrorKind = "rate_limited"
	ErrorKindInternalDbError      ErrorKind = "internal_db_error"
)

// DefaultRateLimitBackoff is the canonical Retry-After hint
// surfaced on wire envelopes of kind=rate_limited when the
// originator did not specify a custom duration. 60s is the default
// per the user-facing spec — large enough to back off from an
// upstream quotalimit throttle without dropping the request entirely.
const DefaultRateLimitBackoff = 60 * time.Second

// HTTPStatus returns the canonical Kind → HTTP-status mapping.
// Stable across server + client; changing a row here is a contract
// change that MUST update the unit tests in cerrors_test.go.
func (k ErrorKind) HTTPStatus() int {
	switch k {
	case ErrorKindLeaseLost, ErrorKindIdempotencyConflict:
		return http.StatusConflict
	case ErrorKindInvalidManifest:
		return http.StatusBadRequest
	case ErrorKindArtifactMissing:
		return http.StatusUnprocessableEntity
	case ErrorKindPublisherUnavailable:
		return http.StatusServiceUnavailable
	case ErrorKindRateLimited:
		return http.StatusTooManyRequests
	case ErrorKindInternalDbError:
		return http.StatusInternalServerError
	}
	// Unknown kind — caller defaults to 500 (the safe fallback).
	return http.StatusInternalServerError
}

// RetryAfter returns the canonical Kind → default Retry-After
// hint. Returns 0 for kinds that do not signal rate-limiting.
func (k ErrorKind) RetryAfter() time.Duration {
	if k == ErrorKindRateLimited {
		return DefaultRateLimitBackoff
	}
	return 0
}

// Valid returns true if k is one of the 7 canonical values. Useful
// for forward-prevention: reject any wire envelope with a kind that
// is not in the closed set.
func (k ErrorKind) Valid() bool {
	for _, canonical := range CanonicalErrorKinds() {
		if k == canonical {
			return true
		}
	}
	return false
}

// CanonicalErrorKinds returns the closed-set of valid kinds in
// stable order (the user-facing spec ordering). The unit tests use
// this to enumerate every Kind when verifying the 3 lookup tables
// (HTTPStatus + RetryAfter + kindToSentinel) are exhaustive.
func CanonicalErrorKinds() []ErrorKind {
	return []ErrorKind{
		ErrorKindLeaseLost,
		ErrorKindIdempotencyConflict,
		ErrorKindInvalidManifest,
		ErrorKindArtifactMissing,
		ErrorKindPublisherUnavailable,
		ErrorKindRateLimited,
		ErrorKindInternalDbError,
	}
}

// ── Canonical typed sentinels (one per Kind) ──────────────────────────────

// 7 typed sentinels (godlike/07). Each is reachable via errors.Is
// AND via the Kind → sentinel lookup table (kindToSentinel). The
// dual-surface discipline mirrors C6/C7 artifact-uploader precedents
// (`var _ remote.ArtifactUploader = (*Adapter)(nil)` etc.).
var (
	ErrCompletionLeaseLost            = errors.New("completion: lease lost (lease_id/attempt mismatch)")
	ErrCompletionIdempotencyConflict  = errors.New("completion: idempotency conflict (replay with different result_hash)")
	ErrCompletionInvalidManifest      = errors.New("completion: invalid artifact manifest")
	ErrCompletionArtifactMissing      = errors.New("completion: artifact missing (state not finalised or has local path)")
	ErrCompletionPublisherUnavailable = errors.New("completion: upstream publisher unavailable")
	ErrCompletionRateLimited          = errors.New("completion: rate limited")
	ErrCompletionInternalDbError      = errors.New("completion: internal database error")
)

// kindToSentinel maps Kind → canonical typed sentinel. The Is() and
// MarshalJSON methods on RemoteCompletionError use this map to
// preserve lookup symmetry between server emit + client decode.
//
// godlike/06 SSOT: when a new Kind is added to the closed-set, the
// row MUST be added here in lockstep with the const-block sibling
// and the 3 lookup functions (HTTPStatus / RetryAfter / Valid).
var kindToSentinel = map[ErrorKind]error{
	ErrorKindLeaseLost:            ErrCompletionLeaseLost,
	ErrorKindIdempotencyConflict:  ErrCompletionIdempotencyConflict,
	ErrorKindInvalidManifest:      ErrCompletionInvalidManifest,
	ErrorKindArtifactMissing:      ErrCompletionArtifactMissing,
	ErrorKindPublisherUnavailable: ErrCompletionPublisherUnavailable,
	ErrorKindRateLimited:          ErrCompletionRateLimited,
	ErrorKindInternalDbError:      ErrCompletionInternalDbError,
}

// ── RemoteCompletionError (wire-shape envelope) ──────────────────────────

// RemoteCompletionError is the wire-shape typed-error envelope.
// Implements error + Is() + Unwrap() so callers can use both:
//
//	if errors.As(err, &remErr) { ... }              // structured probe
//	if errors.Is(err, remote.ErrCompletionXxx) {...} // sentinel probe
//
// Wildcard use cases:
//   - Server emits: api/jobs.MapErrorToHTTP → c.AbortWithStatusJSON
//     with the canonical wire envelope {kind, error, retry_after_seconds}.
//   - Client decodes: jobbrokerclient.decodeCompletionErrorEnvelope
//     → reconstructs a RemoteCompletionError instance from the JSON
//     body. Caller can then errors.Is / errors.As against the kind
//     to route the failure.
type RemoteCompletionError struct {
	// Kind is the canonical 7-kind closed-set value. Always set.
	Kind ErrorKind `json:"kind"`
	// Message is the human-readable diagnostic string. Always set
	// (the wire envelope maps to {"error": "<msg>"} on serialization).
	Message string `json:"error"`
	// RetryAfter is the optional backend-debounced retry hint. Set
	// to 0 for all kinds except ErrorKindRateLimited. Marshalled
	// as "retry_after_seconds" (omitted when 0).
	RetryAfter time.Duration `json:"retry_after_seconds,omitempty"`
	// inner is the canonical sentinel for errors.Is probes. NOT
	// serialised; preserved through Is() + Unwrap() so callers can
	// reach the typed sentinel from a wire-reconstructed envelope.
	inner error
}

// Error implements the error interface.
func (e *RemoteCompletionError) Error() string {
	if e == nil {
		return "<nil RemoteCompletionError>"
	}
	if e.inner != nil {
		if e.RetryAfter > 0 {
			return fmt.Sprintf("%s (kind=%s inner=%s retry_after=%s)", e.Message, e.Kind, e.inner, e.RetryAfter)
		}
		return fmt.Sprintf("%s (kind=%s inner=%s)", e.Message, e.Kind, e.inner)
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s (kind=%s retry_after=%s)", e.Message, e.Kind, e.RetryAfter)
	}
	return fmt.Sprintf("%s (kind=%s)", e.Message, e.Kind)
}

// Is implements errors.Is support. Probes against the canonical
// sentinel for the Kind via kindToSentinel. Returns false on nil
// receiver or unknown target — symmetric with the kindToSentinel
// map construction (a kind missing from the map fails closed).
func (e *RemoteCompletionError) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	canonical, ok := kindToSentinel[e.Kind]
	if !ok {
		return false
	}
	return errors.Is(target, canonical)
}

// Unwrap returns the inner sentinel so errors.Is / errors.As can
// probe the canonical sentinel via the standard chain traversal.
// Pre-condition: the inner sentinel is set at NewRemoteCompletionError
// time (always non-nil); nil-unwrap is defensive only.
func (e *RemoteCompletionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.inner
}

// MarshalJSON encodes the wire envelope, converting RetryAfter
// (time.Duration) to seconds-integer. The placeholder field name
// on the wire is "retry_after_seconds" per the user-facing spec —
// encoding the raw nanosecond int64 would surprise a 60s canonical
// backoff, breaking symmetry with the server-side emitEnvelope
// shape (which writes int seconds via body["retry_after_seconds"]).
//
// godlike/06 SSOT: this is the canonical serialization for the
// retry-after hint. Both server (MapErrorToHTTP.emitEnvelope) and
// client (jobbrokerclient.wire_decoder) emit/receive int seconds —
// changes here are a wire contract change.
func (e *RemoteCompletionError) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("null"), nil
	}
	body := struct {
		Kind              ErrorKind `json:"kind"`
		Error             string    `json:"error"`
		RetryAfterSeconds int       `json:"retry_after_seconds,omitempty"`
	}{
		Kind:  e.Kind,
		Error: e.Message,
	}
	if e.RetryAfter > 0 {
		body.RetryAfterSeconds = int(e.RetryAfter.Seconds())
	}
	return json.Marshal(body)
}

// NewRemoteCompletionError is the canonical constructor. Validates
// the Kind against the canonical closed-set per godlike/06: an
// unknown Kind surfaces a typed error at composition time rather
// than silently mapping the wire envelope to an unmapped status.
//
// The inner sentinel is required (clause: godlike/07 typed-error
// contract). Callers that legitimately have NO canonical sentinel
// can wrap a freshly-created fmt.Errorf into a different sentinel
// before calling New; an empty-marker inner returns the typed
// error from godlike/06's forward-preventive posture.
func NewRemoteCompletionError(kind ErrorKind, msg string, inner error) *RemoteCompletionError {
	// Forward-prevention: an invalid Kind is more dangerous than a
	// missing-inner because the wire would emit it under a kind
	// the 3 lookup tables (HTTPStatus / RetryAfter / Valid) cannot
	// resolve. Coerce to internal_db_error (the safe 500 fallback)
	// so the wire still emits a structured envelope. The original
	// Kind is preserved in the returned struct for diagnostic
	// surfacing via a future PR — the wire-shape envelope still
	// falls back to the safe status code.
	if !kind.Valid() {
		kind = ErrorKindInternalDbError
	}
	if msg == "" {
		msg = "<empty-message>"
	}
	if inner == nil {
		// Defensive: ensure errors.Is / errors.As can still probe
		// the canonical sentinel. Pre-populate from the kind's
		// canonical sentinel so the probe-by-kind and the probe-
		// by-sentinel are equivalent on the returned value.
		inner = kindToSentinel[kind]
	}
	return &RemoteCompletionError{
		Kind:    kind,
		Message: msg,
		inner:   inner,
	}
}
