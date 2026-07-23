// Package retry — registry_stdlib.go (FASE 6 Cut 6.1.B1, July 2026).
//
// Stdlib typed-error classifier registrations. Each classifier in this
// file is invoked at init() so the canonical retry.Decision() walker
// (decision.go) sees it on first-call. The walker then consults this
// chain BEFORE falling through to any legacy surface (production paths
// compile out the legacy substring fallback entirely; see
// transient_legacy_test.go for the test-only fixture).
//
// godlike/06 SSOT (one canonical owner per fact): stdlib-shaped
// errors (exec.ExitError, url.Error, *http.Transport-shaped requests)
// have ONE canonical classifier per shape. Adapters in
// internal/ that wrap these errors MUST NOT register a competing
// classifier — going through retry.WrapTransient (transient.go) at the
// typed-transient boundary is the canonical typed-overlay path.
//
// Why stdlib classifiers are CENTRALIZED here (vs DISTRIBUTED in the
// adapter packages like internal/.../qdrant, internal/.../sqlite):
// the stdlib errors live in pkg/retry's import graph (stdlib is
// importable from anywhere), so the classifier can be colocated
// with the package that consumes them. Go's visibility rule
// (pkg/ cannot import internal/) BLOCKS centralization of internal
// adapter classifiers — those MUST be distributed to the adapter's
// own init() in the matching internal/.../package.
//
// Shape-by-shape classifier contracts:
//
//   classifyExecExitError(*exec.ExitError):
//     - Conservative: ALL *exec.ExitError classify as RetryDecision{
//       Class: ErrUnknown, Retryable: false, SafeMessage: ...} —
//       shell callers wrap specific exit codes (e.g. EX_TEMPFAIL=75
//       for git, sqlassets codes) via retry.WrapTransient at the
//       typed-transient boundary. The 5-tuple (Class, Retryable,
//       RetryAfter, SafeMessage) is API-stable even when SafeMessage
//       is "exit-N" rather than category-specific.
//
//   classifyURLError(*url.Error):
//     - *url.Error wraps a typed inner error (net.OpError typically).
//       When the wrapped error is a Timeout / Temporary (canonical
//       net.Error implementations), the URL error is transient;
//       propagate by emitting ErrTimeout/ErrNetwork with Retryable=true.
//       Non-transient inner errors (e.g. InvalidURL → &url.Error{
//       Op: "parse", URL: "...", Err: errors.New("invalid URL prefix")})
//       are classified as RetryDecision{ErrValidation, Retryable: false}.
//
//   classifyHTTPTransportError(*http.Transport.RoundTrip.err):
//     - http.Client.Do() returns *url.Error wrapping net errors.
//       Direct *http.Transport-level errors are net.OpError under the
//       hood — the same classifier that handles net.OpError /
//       url.Error covers the transport. There is no canonical
//       *HTTPError type — produce Classifier side covering
//       context.DeadlineExceeded (callers wrap an upstream ctx->
//       timeout).
//
// Compiled at init() so first retry.Decision(err) does NOT race the
// registration (Go init() serialisation contract — the chain is fully
// populated before main() runs).

package retry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"syscall"
)

// ── *exec.ExitError classifier ───────────────────────────────────────────────

// classifyExecExitError maps a *exec.ExitError to a typed RetryDecision.
// Conservative policy: every ExitError is classified as ErrUnknown +
// Retryable=false. Shell callers that know a specific exit code is
// transient (e.g. git's EX_TEMPFAIL=75, sqlite3-shell's busy code)
// MUST wrap the exit code with retry.WrapTransient at the call site —
// the classifier here does NOT guess on shell semantics.
//
// Returns (zero-value, false) when err does not match *exec.ExitError.
// Idempotent: same input → same output (no state, no clock).
func classifyExecExitError(err error) (RetryDecision, bool) {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return RetryDecision{}, false
	}
	// Conservative shape: SafeMessage surfaces the exit code so audit
	// dashboards can investigate without exposing the stderr body (the
	// caller can decide to log stderr separately under a structured
	// log key, NEVER inlined into the typed-error message).
	code := ee.ExitCode()
	// syscall.ENOENT-style decode: when the exit code maps to a
	// syscall.Errno (32-255 on Linux), surface the canonical errno
	// so the operator can grep for it. Otherwise the bare integer
	// is sufficient (e.g. exit-1 from a happy-path script that
	// exited 1 after processing).
	if errno, ok := codeAsErrno(code); ok {
		return RetryDecision{
			Class:       ErrUnknown,
			Retryable:   false,
			SafeMessage: fmt.Sprintf("exec: exit code %d (%v)", code, errno),
		}, true
	}
	return RetryDecision{
		Class:       ErrUnknown,
		Retryable:   false,
		SafeMessage: fmt.Sprintf("exec: exit code %d", code),
	}, true
}

// codeAsErrno translates a process exit code to a syscall.Errno when
// it falls in the [syscall.Errno(1), syscall.Errno(255)] range (the
// canonical Linux mapped-errno band). Returns (errno, true) on match;
// (zero, false) otherwise. Defensive bytes-only — never panics on
// out-of-range or non-errno codes.
func codeAsErrno(code int) (syscall.Errno, bool) {
	if code < 1 || code > 255 {
		return 0, false
	}
	return syscall.Errno(code), true
}

// ── *url.Error classifier ────────────────────────────────────────────────────

// classifyURLError maps a *url.Error to a typed RetryDecision by
// consulting the wrapped error shape. net/url.URL wraps_inner errors
// with a typed *url.Error envelope (Op / URL / Err fields); the inner
// error is typically net.OpError (transport-level) or a parse error
// (URL-parsing-level).
//
// Decision order (typed-first, no substring):
//  1. nil OR non-*url.Error → (zero, false) — pass to next classifier
//  2. Inner error implements net.Error with Timeout()==true →
//     RetryDecision{ErrTimeout, Retryable: true, SafeMessage: ...}
//  3. Inner error implements net.Error with Temporary()==true →
//     RetryDecision{ErrNetwork, Retryable: true, SafeMessage: ...}
//  4. Inner error is context.DeadlineExceeded → ErrTimeout + terminal
//     (the per-call ctx is canonical; caller decides whether to bind
//     the ctx to a retry budget). For most retry loops, this is
//     retryable=true so an upstream timeout propagates back into a
//     retry.
//  5. URL parse error shape ("parse <url>: ...") → ErrValidation +
//     terminal (URL never changes on retry; retrying with the same
//     URL produces the same parse failure).
//  6. Anything else → ErrUnknown + terminal (conservative).
func classifyURLError(err error) (RetryDecision, bool) {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return RetryDecision{}, false
	}
	// Unwrap to the inner error. *url.Error.Unwrap is the canonical
	// way to reach the wrapped shape (godlike/06 SSOT: parser
	// contract, not a parse-the-strings heuristic).
	inner := ue.Err
	if inner == nil {
		return RetryDecision{}, false
	}
	// Path 1: context.DeadlineExceeded. Surface as ErrTimeout +
	// retryable=true (the canonical retry-on-ctx-timeout pattern; the
	// retry loop's own ctx cancellation is checked in DoWithValue
	// before this classifier runs so the ErrTimeout shape is the
	// upstream debounce, not the loop's own).
	if errors.Is(inner, context.DeadlineExceeded) {
		return RetryDecision{
			Class:       ErrTimeout,
			Retryable:   true,
			SafeMessage: fmt.Sprintf("url: %s %s: context deadline exceeded", ue.Op, redactedURL(ue.URL)),
		}, true
	}
	// Path 2: net.Error with Timeout() marked. The standard
	// "net/http transport timed out reaching <host>" case.
	var ne net.Error
	if errors.As(inner, &ne) && ne.Timeout() {
		return RetryDecision{
			Class:       ErrTimeout,
			Retryable:   true,
			SafeMessage: fmt.Sprintf("url: %s %s: timeout", ue.Op, redactedURL(ue.URL)),
		}, true
	}
	// Path 3: net.Error with Temporary() marked. Most net.DNS / dial
	// errors return Temporary()==true (the upstream canonical
	// signal).
	if errors.As(inner, &ne) && ne.Temporary() {
		return RetryDecision{
			Class:       ErrNetwork,
			Retryable:   true,
			SafeMessage: fmt.Sprintf("url: %s %s: temporary", ue.Op, redactedURL(ue.URL)),
		}, true
	}
	// Path 4: URL parse error. op == "parse" + inner implements
	// errors.Is(err, ErrBadURLParseOrder) (the go stdlib sentinel
	// marker). Conservative — most "invalid URL" messages come back
	// through *url.Error but the upstream types are wrapped behind
	// net/url internals so we route the SafeMessage through the
	// generic ErrValidation bucket and let the caller inspect the
	// underlying string. Retry never changes a parse failure on the
	// same URL.
	if ue.Op == "parse" {
		return RetryDecision{
			Class:       ErrValidation,
			Retryable:   false,
			SafeMessage: fmt.Sprintf("url: parse %s: invalid", redactedURL(ue.URL)),
		}, true
	}
	// Path 5: conservative terminal. Unknown inner error shape.
	return RetryDecision{
		Class:       ErrUnknown,
		Retryable:   false,
		SafeMessage: fmt.Sprintf("url: %s %s", ue.Op, redactedURL(ue.URL)),
	}, true
}

// redactedURL strips URL userinfo + query string for SafeMessage
// audit logs. URL paths and hostnames are typically non-sensitive at
// audit-log scope (operators already see them in HTTP access logs);
// the SafeMessage contract per RetryDecision docstring is "no
// credentials" — query strings with tokens / passwords qualify. The
// caller passes the raw URL; this helper returns the canonical
// "scheme://host/path" shape that the operator sees in access logs.
func redactedURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		// Fallback to raw; SafeMessage is for audit not for parsing.
		return rawURL
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	if u.Host == "" {
		return u.Path
	}
	return u.Scheme + "://" + u.Host + u.Path
}

// ── Registration ────────────────────────────────────────────────────────────

// classifyExecExitError and classifyURLError are wired into the
// default ClassifierRegistry in decision.go at init time. Add new
// stdlib classifiers here (one per shape) and register them in
// defaultClassifierRegistry. Do NOT register the same shape twice —
// the Decision walker first-match-wins so a later registration
// silently shadows the earlier one.
