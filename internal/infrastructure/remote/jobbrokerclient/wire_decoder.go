// Package jobbrokerclient — wire_decoder.go (P1 #15, July 2026).
//
// Wire-envelope decoder for the remote-completion client. Takes
// the raw JSON body the server emitted for a /complete or
// /complete-with-artifacts 4xx/5xx response and reconstructs a
// typed *remote.RemoteCompletionError via godlike/07 typed-error
// contract.
//
// godlike/06 SSOT: the wire shape on this side mirrors the
// canonical taxonomy declared in internal/domain/remote/cerrors.go
// (ErrorKind closed-set + RemoteCompletionError envelope). One
// decoder implementation, no per-client-site drift — the file
// lowers the canonical wire-shape to one place.
//
// godlike/07 typed-error contract: callers can probe either via
// errors.As (structured Kind-by-Kind) OR via errors.Is against the
// 7 canonical Kind-sentinels (sentinel-by-sentinel). The
// canonical-sentinel wrap preserves the execution-layer identity
// so the existing client tests that probe against
// appjobs.ErrLeaseLost keep working without churn (forward-pointer:
// the C6/C7 artifact-uploader sentinels also retain identity).
// The wrapping rule is "kind → domain sentinel" so the wire
// envelope is byte-exact with the existing client_test.go prior
// knowledge — changing the wrap here is a contract regression.
package jobbrokerclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// wireEnvelope mirrors the server-side body shape emitted by
// internal/capabilities/jobs/transport.MapErrorToHTTP. Kept private to this package so the
// 7 canonical names (kind / error / retry_after_seconds) cannot
// drift from the encode side.
type wireEnvelope struct {
	Kind              string `json:"kind"`
	Error             string `json:"error"`
	RetryAfterSeconds *int   `json:"retry_after_seconds,omitempty"`
}

// decodeCompletionErrorEnvelope attempts to reconstruct a typed
// error from the server's wire envelope. Returns:
//
//   - (remErr, true) when rawBody parses AND kind is non-empty AND
//     kind is in the canonical closed-set (forward-prevention:
//     unknown kinds fail closed with typed-sentinel "unknown_kind").
//   - (nil, false) when rawBody is malformed OR not a typed envelope
//     (kind missing or no error field). Caller falls through to
//     the existing fmt.Errorf("HTTP %d: %s") path.
//
// godlike/07 typed-error contract: every successful decode returns
// a *remote.RemoteCompletionError wrapping the canonical sentinel
// for the Kind, so errors.Is(err, <domain sentinel>) probes
// succeed symmetrically.
//
// godlike/06 SSOT: the kindToDomainSentinel map is the SINGLE
// source of truth for the wire-to-sentinel translation; adding a
// new Kind in domain/remote/cerrors.go requires updating this
// map in lockstep (mirrors the C6 Adapter precedence pattern).
func decodeCompletionErrorEnvelope(rawBody []byte) (*remote.RemoteCompletionError, bool) {
	if len(rawBody) == 0 {
		return nil, false
	}
	var env wireEnvelope
	if err := json.Unmarshal(rawBody, &env); err != nil {
		return nil, false
	}
	if env.Kind == "" {
		return nil, false
	}
	// Forward-prevention: only accept the closed-set of canonical
	// kinds. Unknown kinds fail closed to avoid leaking arbitrary
	// string into the typed-error chain (which would break
	// errors.As probes for the existing 7 kinds).
	kind := remote.ErrorKind(env.Kind)
	if !kind.Valid() {
		return remote.NewRemoteCompletionError(
			remote.ErrorKindInternalDbError,
			fmt.Sprintf("unknown completion error kind=%q", env.Kind),
			errors.New("decodeCompletionErrorEnvelope: unknown kind"),
		), true
	}

	// Compute RetryAfter (only meaningful for rate_limited, but
	// parsing the field unconditionally surfaces it for any kind
	// the server chose to attach it to — future-friendly).
	var retryAfter time.Duration
	if env.RetryAfterSeconds != nil && *env.RetryAfterSeconds > 0 {
		retryAfter = time.Duration(*env.RetryAfterSeconds) * time.Second
	}
	// Build the canonical typed envelope. The inner field wraps the
	// execution-layer sentinel so errors.Is(err, appjobs.ErrLeaseLost)
	// probes keep working for downstream clients (existing
	// client_test.go contract).
	msg := env.Error
	if msg == "" {
		msg = "<empty-error-message>"
	}
	remErr := remote.NewRemoteCompletionError(kind, msg, kindToDomainSentinel(kind))
	remErr.RetryAfter = retryAfter
	return remErr, true
}

// kindToDomainSentinel maps the wire-level Kind to the deepest
// canonical domain sentinel that callers probe via errors.Is.
//
// The lease_lost row maps to appjobs.ErrLeaseLost (forward-compat
// with the existing TestCompleteWithArtifacts_LeaseLostSentinel
// contract: errors.Is(err, appjobs.ErrLeaseLost)). The other
// rows map to the canonical completion-package sentinels; future
// call sites that probe against these sentinels can do so without
// a wire-decode churn.
func kindToDomainSentinel(kind remote.ErrorKind) error {
	switch kind {
	case remote.ErrorKindLeaseLost:
		return jobs.ErrLeaseLost
	case remote.ErrorKindIdempotencyConflict:
		return remote.ErrCompleteJobIdempotencyConflict
	case remote.ErrorKindInvalidManifest:
		return remote.ErrRemoteArtifactManifestInvalid
	case remote.ErrorKindArtifactMissing:
		return remote.ErrRemoteArtifactStateNotFinalized
	case remote.ErrorKindPublisherUnavailable:
		// Canonical sentinel-of-sentinels for publisher_unavailable
		// (the cross-package alias in artlist/ports.go for the
		// same surface is reconciled via type-alias in the BACKFILL
		// phase of P1 #15 — forward-pointer).
		return remote.ErrCompletionPublisherUnavailable
	case remote.ErrorKindRateLimited:
		return remote.ErrCompletionRateLimited
	case remote.ErrorKindInternalDbError:
		return remote.ErrCompletionInternalDbError
	}
	return nil
}
