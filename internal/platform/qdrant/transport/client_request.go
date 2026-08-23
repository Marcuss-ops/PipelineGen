// client_request.go — HTTP request surface for the Qdrant REST client.
//
// PR2 mechanical split (June 2026): relocated from client.go without
// signature or behaviour changes. Every outbound Qdrant request flows
// through prepareRequest (auth header + Content-Type), then either
// doRequest (uses c.httpClient) or DoWithHTTPClient (uses a caller-
// supplied *http.Client — used by HealthProbe.Probe for its own
// per-call Timeout ceiling).
//
// The auth contract (X-Api-Key / api-key header) is centralised here:
// PR1 fix/qdrant-wire-contracts P0.1 collapsed the duplicated
// Content-Type/api-key injection into prepareRequest so every wire
// path carries the same header and the probe no longer reaches
// around to set X-Api-Key by hand.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (c *Client) doJSON(ctx context.Context, method, url string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return c.doRequest(ctx, method, url, bytes.NewReader(data))
}

// prepareRequest is the single source of truth for request headers
// (Content-Type + api-key) — every outbound Qdrant request flows
// through here so the auth contract cannot drift across call sites.
//
// PR1 fix (reviewer feedback): previously `doRequest` and
// DoWithHTTPClient each inlined the Content-Type / api-key
// injection, which meant any drift in the auth contract would have
// to be remembered in both places. The helper is internal-only to
// avoid widening the public Client surface.
func (c *Client) prepareRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// PR1 — fix/qdrant-wire-contracts (P0.1):
	//   the API key is now injected by the single shared transport
	//   so the probe (health.go) and every CRUD endpoint carry the
	//   same header. Health no longer reaches around and sets
	//   X-Api-Key by hand.
	//
	// Qdrant accepts both `api-key` and `X-Api-Key` (HTTP headers
	// are case-insensitive). The canonical lowercase form is what
	// the Qdrant docs reference, so we use that.
	//
	// Trimming: a config-side trailing newline or whitespace would
	// otherwise pass the empty-check and send a polluted header
	// (auth failures with no loggable cause). Trim before comparison
	// and before setting.
	if key := strings.TrimSpace(c.apiKey); key != "" {
		req.Header.Set("api-key", key)
	}
	return req, nil
}

func (c *Client) doRequest(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	req, err := c.prepareRequest(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	return c.httpClient.Do(req)
}

// DoWithHTTPClient performs a request through a caller-supplied
// *http.Client. The api-key header is still injected by this Client
// so the auth contract stays centralised (PR1 — fix/qdrant-wire-contracts,
// P0.1). Used by HealthProbe.Probe so the probe can keep its own
// per-call Timeout ceiling for defense-in-depth even when schema.Config.Timeout
// is large or unset.
func (c *Client) DoWithHTTPClient(ctx context.Context, hc *http.Client, method, url string, body io.Reader) (*http.Response, error) {
	if hc == nil {
		hc = c.httpClient
	}
	req, err := c.prepareRequest(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	return hc.Do(req)
}
