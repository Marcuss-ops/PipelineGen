// Package retry — google_api_error.go (P1.5, July 2026).
//
// Typed-error surface for Google API (and any gRPC-flavoured error
// with HTTP status code semantics). The base path is
// ClassifyGoogleAPIError(err): given a raw error — typically a
// *googleapi.Error returned by Files.Create / Files.Get /
// Files.List / docs.v1.Documents.Create — returns a typed
// *GoogleAPIError envelope that:
//
//	(1) wraps the raw error so errors.Is / errors.As continuity
//	    with the legacy transient-substring taxonomy is preserved
//	    (Unwrap surfaces Raw);
//	(2) categorises the failure into one of 6 typed Kind sentinels
//	    so callers can match "is THIS a 429 throttling event?"
//	    specifically (vs a generic transient) via errors.Is;
//	(3) exposes the HTTP StatusCode and the parsed Retry-After
//	    header (RFC 7231 §7.1.3) for back-pressure decisions —
//	    pkg/retry::DoWithValue honors Retry-After over the
//	    computed backoff via the RetryAfterError interface;
//	(4) implements the canonical RetryableError interface so
//	    retry.IsTransient reaches the typed-path #1 without
//	    substring matching.
//
// Hybrid pattern (struct envelope + sentinel aliases): the canonical
// voiceover/orphan_sweeper.go + delivery/ConflictPolicy shape. Errors.Is
// (err, ErrGoogleAPIThrottled) works because *GoogleAPIError.Is(target
// error) matches against the sentinel the classifier chose.
//
// P1.5 also fills a gap surfaced during P1.3 / P1.4 audits: the
// pre-P1.5 transient classifier on Drive SDK errors relied entirely on
// substring matching via pkg/retry::transientSubstrings. String taxonomy
// drifts on upstream SDK changes (e.g. Google API shapes camelCase vs
// SNAKE_CASE), so the typed carrier is the canonical resolution.
//
// godlike/06 SSOT: parseRetryAfter lives ONLY here. SDK-specific HTTP
// date / delta-seconds parsing is a single canonical helper, called
// from ClassifyGoogleAPIError at the typed wrap boundary.
package retry

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
)

// ── Google API error typed sentinels (errors.Is probes) ────────────────
//
// Each sentinel names an HTTP-status-equivalent class. Callers
// (audit logs, retry policies, dashboards) can match against
// specific kinds via `errors.Is(err, retry.ErrGoogleAPIThrottled)`
// rather than parsing the status code themselves. The matching
// happens through *GoogleAPIError.Is(target).
var (
	// ErrGoogleAPIThrottled — 429 Too Many Requests.
	// Transient. Honors Retry-After (Google API returns
	// Retry-After: <delta-seconds> on throttling responses).
	ErrGoogleAPIThrottled = errors.New("googleapi: throttled (429)")

	// ErrGoogleAPIServer — 5xx (500/501/502/503/504) +
	// 408 Request Timeout. Transient. May honor Retry-After
	// on 503 Service Unavailable.
	ErrGoogleAPIServer = errors.New("googleapi: server error (5xx/408)")

	// ErrGoogleAPIPermission — 403 Forbidden (permission /
	// insufficient / suspended). Terminal. Retrying does not
	// change the auth state.
	ErrGoogleAPIPermission = errors.New("googleapi: permission denied (403)")

	// ErrGoogleAPINotFound — 404 Not Found. Terminal. The
	// resource is gone; retry cannot recreate it via the
	// same ID path (only the parent folder surface might
	// have changed, which is a different invariant).
	ErrGoogleAPINotFound = errors.New("googleapi: not found (404)")

	// ErrGoogleAPIClient — 4xx other than 403/404/408 (400 Bad
	// Request, 401 Unauthorized, 409 Conflict). Terminal.
	// Invalid requests don't fix themselves on retry.
	ErrGoogleAPIClient = errors.New("googleapi: client error (400/401/409)")

	// ErrGoogleAPIUnknown — unrecognised shape (off-spec *googleapi.Error
	// without an in-range status code, OR non-*googleapi.Error that
	// ClassifyGoogleAPIError could not type-assert). IsRetryable returns
	// false on Unknown. Outer retry loops that want to recover a
	// transient classification for non-typed errors MUST route through
	// retry.WrapTransient at the typed-transient boundary — the
	// pure-substring retry.IsTransient classifier was REMOVED in
	// FASE 6 Cut 6.1.D (per godlike/07 typed-only taxonomy).
	ErrGoogleAPIUnknown = errors.New("googleapi: unknown")
)

// ── *GoogleAPIError typed envelope ──────────────────────────────────────

// GoogleAPIError is the typed envelope for Google API errors.
// Constructed exclusively via ClassifyGoogleAPIError — never directly.
// Carries the raw error for errors.Is/As chains, exposes structured
// fields (StatusCode, Kind, RetryAfter), and satisfies:
//
//   - RetryableError (typed-path #1 of retry.IsTransient)
//   - RetryAfterError (retry.go::DoWithValue honors the header)
//
// Fields are exported so audit-log callers can introspect without
// re-parsing the body. Body is intentionally NOT truncated here;
// the caller (log line) decides truncation policy.
type GoogleAPIError struct {
	// Kind is the typed sentinel (ErrGoogleAPIThrottled /
	// Server / Permission / NotFound / Client / Unknown).
	// errors.Is probes against this field via
	// *GoogleAPIError.Is.
	Kind error

	// StatusCode is the HTTP status the raw *googleapi.Error
	// carried. Zero when the classifier fell back to substring
	// matching on non-*googleapi.Error string (e.g. an upstream
	// fmt.Errorf that lost the type info).
	StatusCode int

	// RetryAfter is the parsed Retry-After header
	// (RFC 7231 §7.1.3: delta-seconds OR HTTP-date). Zero when
	// the response did not carry a Retry-After header. Honors
	// the larger of (computed backoff, retryAfter) in
	// DoWithValue via the RetryAfterError interface.
	RetryAfter time.Duration

	// Body is the raw response body string for ops log
	// inspection. Useful in audit pipelines (which destination_id
	// failed, what did the API say). The caller decides
	// truncation policy.
	Body string

	// Raw is the upstream error (*googleapi.Error typically).
	// Unwrap exposes this to errors.Is/As chains. Per FASE 6
	// Cut 6.1.D, the substring-path fallback was REMOVED from the
	// retry classifier; classification is now typed-only. Callers
	// that want RetryAfterError continuity through wraps still
	// get that for free via errors.As on the *GoogleAPIError
	// envelope — Unwrap is for raw upstream-chain recovery.
	Raw error
}

// Error returns the wrapped raw error's string for log surfaces.
// The canonical convention in the codebase is for the "outermost"
// error's Error() to surface useful context — here, the upstream
// *googleapi.Error already carries the HTTP status line which is
// the most useful identifier. Recipients that want typing
// information can errors.As for *GoogleAPIError and read
// .Kind / .StatusCode.
func (e *GoogleAPIError) Error() string {
	if e == nil || e.Raw == nil {
		return "googleapi: <nil>"
	}
	return e.Raw.Error()
}

// Unwrap exposes the raw upstream error for errors.Is / errors.As
// chains. Canonical godlike/07 contract.
func (e *GoogleAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Raw
}

// Is matches against the canonical Kind sentinel. This is what
// makes `errors.Is(err, retry.ErrGoogleAPIThrottled)` work
// end-to-end even when callers propagate the error through
// multiple fmt.Errorf %w layers. The matching is sentinel-equality
// (==), not substring — the retry classifier already settled on
// the right Kind.
func (e *GoogleAPIError) Is(target error) bool {
	if e == nil || e.Kind == nil || target == nil {
		return false
	}
	return e.Kind == target
}

// IsRetryable satisfies the canonical RetryableError interface,
// which is typed-path #1 of retry.IsTransient. Throttled + Server
// kinds are retryable; Permission, NotFound, Client, Unknown are
// NOT retryable.
//
// FASE 6 Cut 6.1.D (July 2026): the layer-2 substring fallback
// (transientSubstrings, delegated via retry.IsTransientString) was
// REMOVED from the retry classifier. Classification is now
// typed-only (godlike/07). A malformed-shape Unknown envelope is
// therefore terminal at THIS classifier level — if the upstream
// status code is unrecoverable from a non-*googleapi.Error shape,
// the operator must add typed classification upstream rather than
// relying on a substring fallback. Test
// TestGoogleAPIError_NoSubstringClassifierReachable pins this
// invariant.
func (e *GoogleAPIError) IsRetryable() bool {
	if e == nil {
		return false
	}
	return e.Kind == ErrGoogleAPIThrottled || e.Kind == ErrGoogleAPIServer
}

// RetryAfterDuration implements the RetryAfterError interface in
// pkg/retry so DoWithValue can honor the upstream Retry-After
// header. Returns zero when the response had no header — the
// retry loop just uses the computed backoff in that case.
func (e *GoogleAPIError) RetryAfterDuration() time.Duration {
	if e == nil {
		return 0
	}
	return e.RetryAfter
}

// ── Classifier (used at adapter exit) ──────────────────────────────────

// classifyGoogleAPIErrorInfo maps a raw *googleapi.Error to the
// canonical Kind + parsed StatusCode + parsed Retry-After (zero
// when no header is present). Returns ErrGoogleAPIUnknown for an
// off-spec error without a recognised status code. The nil guard
// is defense-in-depth — the entrypoint (ClassifyGoogleAPIError) has
// already nil-checked via errors.As.
func classifyGoogleAPIErrorInfo(rawErr *googleapi.Error) (kind error, statusCode int, retryAfter time.Duration) {
	if rawErr == nil {
		return ErrGoogleAPIUnknown, 0, 0
	}
	statusCode = rawErr.Code
	// Status code 0 (malformed response, e.g. raw transport error
	// that lost the upstream status) maps to ErrGoogleAPIUnknown
	// with retryable=false. The retry path treats it as terminal;
	// operators see the broken response in the log. This is
	// intentionally stricter than the substring-fallback path
	// (which would still call 0 → transient via the legacy
	// 429/503 substring taxonomy if the Body contained those
	// tokens — but a malformed response typically has no body
	// either).
	if rawErr.Header != nil {
		if ra := rawErr.Header.Get("Retry-After"); ra != "" {
			retryAfter = parseRetryAfter(ra, time.Now())
		}
	}
	switch {
	case statusCode == http.StatusTooManyRequests:
		return ErrGoogleAPIThrottled, statusCode, retryAfter
	case statusCode == http.StatusRequestTimeout,
		statusCode >= http.StatusInternalServerError && statusCode <= 599:
		return ErrGoogleAPIServer, statusCode, retryAfter
	case statusCode == http.StatusForbidden:
		return ErrGoogleAPIPermission, statusCode, 0
	case statusCode == http.StatusNotFound:
		return ErrGoogleAPINotFound, statusCode, 0
	case statusCode >= http.StatusBadRequest && statusCode <= 499:
		// Off-spec 4xx codes (418 I'm-a-teapot, 426 Upgrade
		// Required, 451 Unavailable-For-Legal-Reasons, etc.) are
		// intentionally bucketed with Client. They share the
		// terminal "request shape is wrong" semantics — retrying
		// with the same parameters will not change the upstream's
		// response. ErrGoogleAPIUnknown is reserved for codes
		// OUT of the 4xx/5xx ranges OR a zero/empty code (the
		// default branch below). Some test cases (e.g. the 418
		// expectation in TestClassifyGoogleAPIError_StatusCodeMapping)
		// pin this convention.
		return ErrGoogleAPIClient, statusCode, 0
	}
	return ErrGoogleAPIUnknown, statusCode, 0
}

// parseRetryAfter parses an RFC 7231 §7.1.3 Retry-After value.
// The header can be either delta-seconds (a small non-negative
// integer) or an HTTP-date (RFC 1123 + RFC 850 + asctime net/http
// accepts all three via http.ParseTime).
//
// Returns 0 for an unparseable value (defensive — operators can
// still see retry shapes, just without the Retry-After hint).
//
// Negative seconds are clamped to 0 (defensive against upstream
// bug). HTTP-dates that resolve to a past time relative to now
// (server said "retry at <past instant>") also return 0 — by the
// time we read the response, the suggested instant has passed.
func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	// Path 1: delta-seconds (canonical for Google API).
	if secs, err := strconv.Atoi(value); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	// Path 2: HTTP-date (RFC 7231 §7.1.3 alternative form).
	if t, err := http.ParseTime(value); err == nil {
		d := t.Sub(now)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

// ClassifyGoogleAPIError is the canonical adapter-exit classifier.
// Callers wrap the raw *googleapi.Error at the SDK exit boundary so
// farther-up retry loops can surface the structured envelope via
// the typed-path #1 in retry.IsTransient + the RetryAfterError
// honoring in DoWithValue.
//
// Returns the wrapped *GoogleAPIError, OR nil if err is nil, OR
// the existing err unchanged when it doesn't match the typed path
// (non-typed errors propagate verbatim — WrapTransient at retry.go
// is the canonical typed-wrap for those).
//
// Idempotency: errors.As walks the chain; an already-typed
// *GoogleAPIError returns unchanged (no double-wrap).
//
// Nil-safety: nil err returns nil.
//
// Typical usage at the adapter exit (e.g. uploader_doPutFile):
//
//	created, err := u.Service.Files.Create(file).Context(ctx).Do()
//	if err != nil {
//	    return nil, retry.ClassifyGoogleAPIError(err)
//	}
func ClassifyGoogleAPIError(err error) error {
	if err == nil {
		return nil
	}
	// Path 1: already-typed envelope. Idempotent rewrap returns
	// the same value rather than stacking wrappers.
	var existing *GoogleAPIError
	if errors.As(err, &existing) {
		return err
	}
	// Path 2: typed *googleapi.Error → wrap into the envelope.
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		kind, code, retryAfter := classifyGoogleAPIErrorInfo(gerr)
		return &GoogleAPIError{
			Kind:       kind,
			StatusCode: code,
			RetryAfter: retryAfter,
			Body:       gerr.Body,
			Raw:        err,
		}
	}
	// Path 3: non-typed error. Preserve the upstream shape so
	// outer retry loops see exactly what the SDK returned (the
	// canonical retry.WrapTransient at retry.go is for ESS
	// boundary typed wrapping; this function is the googleapi
	// boundary typed wrapping, distinct but adjacent). Callers
	// that want the substring-path classifier can route through
	// retry.WrapTransient(err) directly instead.
	return err
}
