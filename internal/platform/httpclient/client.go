// Package httpclient — canonical HTTP client port + default adapter
// (PR-REFACTOR-P0-IO-BINDER-HTTP, July 2026; migrated from application/ports 2026-08-24).
//
// godlike/06 SSOT: this package is the canonical owner of both the
// Client interface (the narrow HTTP port consumed by application-layer
// use cases) and the DefaultClient concrete adapter (the sole *http.Client
// constructor). Application callers import this package for the port type
// and receive a concrete Client from the composition root.
//
// godlike/07 fail-closed: DefaultClient is the ONLY type in this
// package that constructs a *http.Client directly. Application-layer
// callers MUST NOT construct *http.Client inline; they receive a
// Client from the composition root and pass it to use cases via
// constructor injection.
//
// Concurrency: the underlying *http.Client is documented safe for
// concurrent use; this contract is preserved across the adapter.
package httpclient

import (
	"context"
	"io"
	"net/http"
	"time"
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

// DefaultClient is the canonical implementation of ports.Client that
// wraps a real *http.Client. The timeout is set once at construction;
// callers needing per-call timeouts should set them via
// http.NewRequestWithContext (the timeout here is the upper bound
// for the entire request lifecycle).
type DefaultClient struct {
	http *http.Client
}

// NewDefaultClient constructs a DefaultClient with the given timeout.
// timeout=0 means no timeout (http.Client default; generally not
// recommended — pick a sane upper bound for the use case).
//
// godlike/07 fail-closed: this constructor is the canonical entry
// point for *http.Client construction in the application-facing
// surface. Direct `&http.Client{Timeout: ...}` literals in application
// code are a godlike/07 violation tracked by the IO-BINDER audit.
func NewDefaultClient(timeout time.Duration) *DefaultClient {
	return &DefaultClient{http: &http.Client{Timeout: timeout}}
}

// HTTPClient returns the underlying *http.Client for callers that
// need to pass it to a third-party interface which does not yet
// accept ports.Client (e.g., the image retrieval providers in
// internal/capabilities/images/retrieved/ — those still take
// *http.Client because their interface contract pre-dates this
// port). Use sparingly — preferring ports.Client is the canonical
// pattern; HTTPClient() is an escape hatch for the migration
// window, not a permanent surface.
//
// Future PRs (PR-IO-BINDER-HTTP follow-ups) will incrementally widen
// the retrieval providers to take ports.Client directly; once that
// lands, callers can drop this escape hatch.
func (d *DefaultClient) HTTPClient() *http.Client { return d.http }

// Do executes the request via the underlying *http.Client. Mirrors
// http.Client.Do. Callers must close resp.Body when done.
func (d *DefaultClient) Do(req *http.Request) (*http.Response, error) {
	return d.http.Do(req)
}

// Post issues an HTTP POST with the given context + contentType +
// body. Mirrors http.Client.Post semantics with explicit context
// (so the caller's ctx cancellation cleanly terminates the request).
func (d *DefaultClient) Post(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return d.http.Do(req)
}

// Get issues an HTTP GET. Mirrors http.Client.Get semantics with
// explicit context.
func (d *DefaultClient) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return d.http.Do(req)
}

// Compile-time identity lock (godlike/06 SSOT — drift-detection).
var _ Client = (*DefaultClient)(nil)
