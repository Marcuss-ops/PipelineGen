// Package ports — HTTP client port (PR-REFACTOR-P0-IO-BINDER-HTTP, July 2026).
//
// godlike/04 architectural-binder: this file is the canonical narrow port
// for outbound HTTP requests made by application-layer use cases. Per
// AGENTS.md "internal/application owns use cases and typed ports. It
// must not depend directly on infrastructure implementations", every
// outbound HTTP call from the application layer MUST route through this
// port. The concrete adapter lives at internal/infrastructure/httpclient/
// (PR-REFACTOR-P0-IO-BINDER-HTTP wires the default adapter; tests inject
// a roundtripper-backed fake that records requests and returns canned
// responses).
//
// godlike/07 minimum-blast-radius: the surface is intentionally narrow
// (Do/Post/Get only). Application code never needs more than these
// three shapes today; a richer surface (Cookies, Jar, RoundTripper,
// Transport) would re-introduce the "application knows about transport
// concerns" anti-pattern this port is designed to eliminate.
//
// Out of scope (deferred to separate PRs):
//   - httpport.ClientWithCookies / ClientWithJar — YAGNI today.
//   - httpport.RetryableClient — orthogonal concern; future PR.
//   - Replacing the underlying *http.Client with a higher-fidelity
//     transport (e.g., a custom roundtripper for HMAC-signed POSTs):
//     still routes through ports.Client; out of scope for this PR.
package ports

import (
	"context"
	"io"
	"net/http"
)

// Client is the canonical narrow port for outbound HTTP requests.
//
// Errors: callers follow the standard Go http.Client error contract —
// non-nil on network failure, on build request failure, or on context
// cancellation. Do returns the raw *http.Response; callers MUST close
// resp.Body when done (this is the http.Client contract; the port does
// not hide it).
//
// Concurrency: the underlying *http.Client is documented safe for
// concurrent use; this contract is preserved across the port. Callers
// may share a single Client across goroutines freely.
type Client interface {
	// Do executes an HTTP request and returns its response.
	// Mirrors http.Client.Do. Callers must close resp.Body when done.
	Do(req *http.Request) (*http.Response, error)

	// Post issues an HTTP POST with the given content-type and body.
	// The ctx is propagated to the underlying request via
	// http.NewRequestWithContext. An empty contentType is allowed
	// (the Content-Type header is omitted in that case — useful for
	// receivers that sniff the body). Mirrors http.Client.Post
	// semantics with explicit context.
	Post(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error)

	// Get issues an HTTP GET. The ctx is propagated to the underlying
	// request via http.NewRequestWithContext. Mirrors http.Client.Get
	// semantics with explicit context.
	Get(ctx context.Context, url string) (*http.Response, error)
}
