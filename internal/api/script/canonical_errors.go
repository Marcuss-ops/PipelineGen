// Package script — canonical_errors.go is the canonical SSOT owner
// (godlike/06 one-owner-per-fact) for translating script-domain typed
// errors into HTTP responses.
//
// RED-6 SCRIPT-T03-001 closure (PHASE-9-BUG-REMEDIATION-2026-07-04):
// typed `*domainScript.PlanInvalidError` (and its siblings used during
// the unified envelope path) used to bubble up as HTTP 500 because
// handler sites passed the error directly to apiutil.InternalError or
// wrote `c.JSON(500, ...)`. Per godlike/07 typed-error contract, those
// 5xx leak the internal error string AND the wrong status semantic:
// client-side validation failures must surface as 4xx so downstream
// callers (UI, retry bots, queue workers) can branch on the right code.
//
// Both mapping helpers handle the typed-envelope shape (errors.As) AND
// the bare sentinel (errors.Is) so wrapped errors through fmt.Errorf
// %w chains still classify correctly. CanonicalErrorMessage exposes
// diagnostic detail for typed 4xx errors (ItemID / Details are SHOWABLE
// because they reveal client-bad input) and obfuscates anything that
// falls into the 500 bucket so we never leak stack/file paths to the wire.
package script

import (
	"errors"
	"net/http"

	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// CanonicalHTTPStatus returns the mapped HTTP status for a script error.
// Nil error returns 200 to keep the signature symmetric for callers that
// pass through the same mapper. Typed 4xx surfaces for known
// client-caused sentinels (ErrPlanInvalid / ErrNoSource /
// ErrSourceResolutionFailed). All other errors fall back to 500 —
// godlike/07 fail-closed: the canonical 5xx is the safe default when we
// don't recognize the typed surface.
//
// typed envelopes for GenerationError / PostprocessError intentionally
// fall through to the 500 default because OLlama / TTS / Drive failures
// are server-side concerns (the client didn't do anything wrong). The
// godlike/07 typed-error contract keeps their canonical 5xx routing so
// ops dashboards surface real service-impact incidents under the
// correct status class.
func CanonicalHTTPStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}

	// errors.As catches typed envelopes; errors.Is catches bare sentinels
	// + wrappers via fmt.Errorf %w.
	var (
		planErr   *domainScript.PlanInvalidError
		noSrcErr  *domainScript.NoSourceError
		srcResErr *domainScript.SourceResolutionError
	)

	switch {
	case errors.As(err, &planErr), errors.Is(err, domainScript.ErrPlanInvalid):
		return http.StatusBadRequest
	case errors.As(err, &noSrcErr), errors.Is(err, domainScript.ErrNoSource):
		return http.StatusBadRequest
	case errors.As(err, &srcResErr), errors.Is(err, domainScript.ErrSourceResolutionFailed):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// CanonicalErrorMessage returns the safe-to-display error string for
// the wire. Typed 4xx errors expose their canonical message (ItemID +
// Details are safe to show — they're client-side cause-of-failure
// diagnostics, NOT server internals). Anything 5xx returns the opaque
// "internal server error" so internal struct fields / wrapped call
// chains / filepaths never bubble to the HTTP response.
func CanonicalErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if CanonicalHTTPStatus(err) < http.StatusInternalServerError {
		return err.Error()
	}
	return "internal server error"
}
