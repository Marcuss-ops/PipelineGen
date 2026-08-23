// Package completion — map_error.go (P1 #15, July 2026).
//
// Server-side HTTP mapping that translates execution-layer typed
// errors to the canonical 7-kind wire envelope. Replaces the
// prior apiutil.InternalError call on the /complete and
// /complete-with-artifacts routes (handler_workers.go).
// Specifically, the helper:
//
//  1. Walks the error chain via errors.As for *RemoteCompletionError
//     AND errors.Is against the canonical domain sentinels
//     (ErrConcurrentLeaseRefutation etc.). The two probe styles
//     are symmetric — both resolve to the same canonical Kind.
//
//  2. Emits the canonical wire envelope via c.AbortWithStatusJSON:
//     {"kind": "<kind>", "error": "<msg>", "retry_after_seconds": N}
//     For kind=rate_limited only, emits a Retry-After header per
//     the user-facing spec (429 + Retry-After).
//
//  3. Returns true when the error was mapped (caller MUST return
//     early); returns false when err is not a typed completion error
//     (caller MUST fall through to apiutil.InternalError so the
//     500 path stays intact for genuine unknowns).
//
// godlike/06 SSOT: this file is the canonical glue between the
// execution-layer typed errors (ErrConcurrentLeaseRefutation,
// ErrRemoteArtifactManifestInvalid, …) and the wire envelope
// (defined in domain/remote/cerrors.go). One translation point
// surface, no drift across the map_error sites.
//
// godlike/07 typed-error contract: errors.Is from the upstream
// callers is preserved by errors.Is / errors.As traversal on the
// err argument. The wire envelope does NOT swallow the inner
// sentinel's identity, so callers can still probe via both probes
// after MapErrorToHTTP sets the response.
package transport

import (
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
)

// MapErrorToHTTP writes the canonical completion-error envelope to
// the gin context when err matches one of the 7 canonical kinds.
// Returns true when the error was mapped (caller MUST return early);
// returns false when err does not match any canonical kind (caller
// MUST fall through to apiutil.InternalError).
//
// nil-receiver → false. nil-error → false (defensive; the helper
// is not a generic 200-OK responder — use apiutil.OK for that).
//
// Wire format emitted:
//
//	{ "kind": "<kind>", "error": "<msg>" }
//
// plus {"retry_after_seconds": N} when e.RetryAfter > 0.
//
// The Retry-After HTTP header is set ONLY for kind=rate_limited
// per the user-facing spec; other kinds do not surface the header
// (the body field carries the same hint onto a kind-aware probe).
func MapErrorToHTTP(c *gin.Context, err error) bool {
	if c == nil || err == nil {
		return false
	}
	// Walk 1: errors.As for an already-wrapped RemoteCompletionError.
	// Callers that construct one directly (forward pointer: retry
	// classifier or rate-limit middleware) get to skip the kind-
	// from-sentinel translation step.
	var remErr *remote.RemoteCompletionError
	if errors.As(err, &remErr) {
		emitEnvelope(c, remErr)
		return true
	}
	// Walk 2: errors.Is against the canonical execution-layer
	// typed sentinels. These are the surfaces completion.Service
	// already returns today — the map keeps the existing error
	// chain mono-idempotent (the original typed sentinel is
	// preserved in the dispatched envelope's inner field for
	// downstream errors.Is probes).
	switch {
	case errors.Is(err, remote.ErrConcurrentLeaseRefutation):
		return mapTo(c, remote.ErrorKindLeaseLost, err, 0)
	case errors.Is(err, remote.ErrCompleteJobIdempotencyConflict):
		return mapTo(c, remote.ErrorKindIdempotencyConflict, err, 0)
	case errors.Is(err, remote.ErrRemoteArtifactManifestInvalid):
		return mapTo(c, remote.ErrorKindInvalidManifest, err, 0)
	case errors.Is(err, remote.ErrRemoteArtifactStateNotFinalized),
		errors.Is(err, remote.ErrRemoteArtifactHasLocalPath):
		return mapTo(c, remote.ErrorKindArtifactMissing, err, 0)
	case errors.Is(err, remote.ErrArtifactUploaderNotConfigured),
		errors.Is(err, remote.ErrArtifactSessionNotFound):
		// publisher_unavailable kind: covers the ArtifactUploader
		// port UNCONFIGURED + session-not-found typed sentinels
		// (canonical godlike/06 owners in domain/remote). The artlist
		// delivery-publisher port (artlist/ports.go) sentinel for
		// the same surface lives outside domain/remote; the canonical
		// reconciliation is deferred to a future PR (forward-pointer:
		// `ErrPublisherUnavailable` in artlist/ports.go will be
		// collapsed into `ErrCompletionPublisherUnavailable` via
		// type-alias during the BACKFILL phase of P1 #15).
		return mapTo(c, remote.ErrorKindPublisherUnavailable, err, 0)
	case errors.Is(err, remote.ErrCompletionRateLimited):
		return mapTo(c, remote.ErrorKindRateLimited, err, remote.DefaultRateLimitBackoff)
	case errors.Is(err, remote.ErrCompletionInternalDbError):
		return mapTo(c, remote.ErrorKindInternalDbError, err, 0)
	}
	return false
}

// mapTo is the dispatch shim: wraps err as a RemoteCompletionError
// for the given Kind + retry hint, then emits to the gin context.
// Always returns true on success; c is expected non-nil (the
// caller-side guard at MapErrorToHTTP rejects nil).
func mapTo(c *gin.Context, kind remote.ErrorKind, err error, retryAfter time.Duration) bool {
	remErr := remote.NewRemoteCompletionError(kind, errMessage(err), err)
	remErr.RetryAfter = retryAfter
	emitEnvelope(c, remErr)
	return true
}

// emitEnvelope serialises the typed envelope to the gin context.
// Sets the canonical HTTP status + Retry-After header (for
// rate_limited) + body envelope. Aborts the gin chain so a
// follow-up apiutil.OK / InternalError call is short-circuited.
func emitEnvelope(c *gin.Context, e *remote.RemoteCompletionError) {
	status := e.Kind.HTTPStatus()
	if e.RetryAfter > 0 {
		c.Header("Retry-After", fmt.Sprintf("%d", int(e.RetryAfter.Seconds())))
	}
	body := gin.H{
		"kind":  string(e.Kind),
		"error": e.Message,
	}
	if e.RetryAfter > 0 {
		body["retry_after_seconds"] = int(e.RetryAfter.Seconds())
	}
	c.AbortWithStatusJSON(status, body)
}

// errMessage extracts the canonical error string for the wire
// envelope's "error" field. err.Error() can include percent signs,
// control chars, or large multi-line diagnostics; we surface the
// raw string verbatim per godlike/07 no-fake-availability (the
// client's human-readable rendering must mirror the server's
// failure mode byte-exactly).
func errMessage(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
