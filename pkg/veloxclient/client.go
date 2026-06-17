package veloxclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Client is an HTTP client for the PipelineGen server. Safe for concurrent
// use across goroutines; the underlying http.Client is shared and stateless
// across requests (Transport handles connection pooling).
type Client struct {
	baseURL     string
	token       string
	httpClient  *http.Client
	maxAttempts int
	retryBase   time.Duration
	insecureTLS bool
}

// Option configures the Client at construction time.
type Option func(*Client)

// WithInsecureTLS disables TLS certificate verification. Use only for
// internal clusters where the operator controls both ends of the connection
// and a self-signed cert is in play — production deployments should use
// proper CA-signed certs terminated by a reverse proxy (Caddy / Nginx).
func WithInsecureTLS() Option {
	return func(c *Client) { c.insecureTLS = true }
}

// WithTimeout sets the per-request HTTP timeout. Defaults to 30s.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.httpClient.Timeout = d }
}

// WithMaxAttempts overrides the automatic retry budget. Default is
// DefaultMaxAttempts (3: initial + 2 retries). Set to 1 to disable retries.
func WithMaxAttempts(n int) Option {
	return func(c *Client) { c.maxAttempts = n }
}

// WithRetryBase overrides the exponential backoff base. Default is
// DefaultRetryBase (200ms). The schedule is base, 2*base, 4*base, ...
func WithRetryBase(d time.Duration) Option {
	return func(c *Client) { c.retryBase = d }
}

// New builds a Client. baseURL is the pipelinegen root, e.g.
// "https://pipeline.example.com" or "http://127.0.0.1:18080". If the
// caller passes an unprefixed host:port like "127.0.0.1:18080", an
// "http://" scheme is added automatically so URL composition doesn't
// silently produce unparseable requests. token is the bearer credential
// (must match VELOX_ADMIN_TOKEN or VELOX_WORKER_TOKEN on the server).
func New(baseURL, token string, opts ...Option) *Client {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = "http://127.0.0.1:18080"
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	c := &Client{
		baseURL:     baseURL,
		token:       strings.TrimSpace(token),
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		maxAttempts: DefaultMaxAttempts,
		retryBase:   DefaultRetryBase,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.insecureTLS {
		// Clone http.DefaultTransport rather than constructing a bare
		// &http.Transport{}: the defaults carry keep-alive pooling, dial
		// timeouts, proxy settings, and TLS handshake timeouts that a
		// fresh transport doesn't get. Only swap the TLS config.
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit opt-in: internal clusters + self-signed certs only
		c.httpClient.Transport = tr
	}
	return c
}

// SubmitAsync POSTs payload (encoded as JSON) to path and returns the
// enqueue response. path is a server-relative URL such as
// "api/script/generate-with-images" — the Client prefixes the base URL.
//
// reqID is the X-Request-ID header, which the server uses to enforce
// (type, correlation_id) idempotency. If reqID is empty, a fresh random
// hex ID is generated and propagated as both the X-Request-ID header and
// the resulting job's correlation_id. The auto-generated ID survives the
// client's internal retry attempts, so a network-level 5xx followed by
// a successful retry still converges on the same job.
//
// Returns ErrUnauthorized, ErrBadRequest, ErrServer, or a wrapped raw
// error. On successful retry, the parsed AsyncResponse is returned
// without surfacing the intermediate 5xx to the caller.
func (c *Client) SubmitAsync(ctx context.Context, path string, payload any, reqID string) (*AsyncResponse, error) {
	if strings.TrimSpace(reqID) == "" {
		var err error
		reqID, err = generateRequestID()
		if err != nil {
			return nil, fmt.Errorf("veloxclient: generate request id: %w", err)
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("veloxclient: marshal payload: %w", err)
	}
	url := c.baseURL + "/" + strings.TrimLeft(path, "/")
	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		raw, retryable, err := c.doRequest(ctx, http.MethodPost, url, body, reqID)
		if err == nil && !retryable {
			// Success: decode the response body. If the body isn't valid
			// JSON we surface a wrapped error rather than guessing the
			// server's shape — corrupt-response errors are non-retryable
			// because retrying won't change the bytes.
			var ar AsyncResponse
			if uerr := json.Unmarshal(raw.body, &ar); uerr != nil {
				return nil, fmt.Errorf("veloxclient: decode response: %w (body=%s)", uerr, truncate(raw.body, 256))
			}
			return &ar, nil
		}
		lastErr = err
		if !retryable || attempt == c.maxAttempts {
			break
		}
		// Exponential backoff: base, 2*base, 4*base, ...
		delay := c.retryBase * time.Duration(math.Pow(2, float64(attempt-1)))
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("veloxclient: ctx cancelled during backoff: %w", ctx.Err())
		case <-time.After(delay):
		}
	}
	if lastErr == nil {
		lastErr = ErrServer
	}
	return nil, lastErr
}

// GetJobStatus GETs /api/jobs/{jobID}/full and parses the status fields
// workers typically need. Single attempt (no retry): a missing job is
// almost always a typo, not a transient failure worth retrying.
func (c *Client) GetJobStatus(ctx context.Context, jobID string) (*JobStatusResponse, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("veloxclient: empty jobID")
	}
	url := c.baseURL + "/api/jobs/" + jobID + "/full"
	// doRequest for GETs — but our doRequest always sends Authorization +
	// X-Request-ID headers, which we don't particularly want to vary per
	// endpoint. The X-Request-ID for a GET is informational only (used for
	// request log correlation), not for idempotency.
	resp, retryable, err := c.doRequest(ctx, http.MethodGet, url, nil, "")
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if retryable {
		// Server indicated transient failure on GET — surface without wrapping
		// as ErrServer so the caller can decide whether to retry.
		return nil, ErrServer
	}
	var out JobStatusResponse
	if err := json.Unmarshal(resp.body, &out); err != nil {
		return nil, fmt.Errorf("veloxclient: decode job status: %w", err)
	}
	return &out, nil
}

// rawResponse carries the parsed-or-raw body of a doRequest call. When the
// status is OK we let the caller parse it; for error paths we keep the body
// here so the caller's error wrapping can include a sanitised snippet of
// the server's text.
type rawResponse struct {
	body []byte
}

// redactCredentials scans the body for anything that looks like a bearer
// credential or a long hex/key blob and replaces it with [REDACTED] before
// the snippet is included in any error message. A misconfigured server may
// echo the inbound Authorization header in its 4xx body, and we don't want
// to silently leak that into operator logs via the client.
func redactCredentials(b []byte) []byte {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-+/=]{8,}`),
		regexp.MustCompile(`(?i)token[=:]\s*[A-Za-z0-9._\-+/=]{16,}`),
		regexp.MustCompile(`(?i)(authorization|x-velox-admin-token|x-velox-worker-token)["':= ]+[A-Za-z0-9._\-+/=]{8,}`),
	}
	out := b
	for _, p := range patterns {
		out = p.ReplaceAll(out, []byte("[REDACTED]"))
	}
	return out
}

// doRequest executes one HTTP attempt. The triple-result pattern is:
//   - (resp, false, nil) on 2xx — body returned, not retryable.
//   - (nil, true, ErrServer) on 5xx / network — retryable, caller loops.
//   - (nil, false, ErrUnauthorized|ErrBadRequest|ErrNotFound) on 4xx —
//     not retryable, the caller surfaces immediately.
func (c *Client) doRequest(ctx context.Context, method, url string, body []byte, reqID string) (*rawResponse, bool, error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, false, fmt.Errorf("veloxclient: build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if reqID != "" {
		req.Header.Set("X-Request-ID", reqID)
	}
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Network-level error: retryable.
		return nil, true, fmt.Errorf("%w: %v", ErrServer, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, fmt.Errorf("%w: read body: %v", ErrServer, err)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return &rawResponse{body: raw}, false, nil
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, false, fmt.Errorf("%w: status=%d body=%s", ErrUnauthorized, resp.StatusCode, truncate(raw, 256))
	case resp.StatusCode == 404:
		return nil, false, fmt.Errorf("%w: status=%d", ErrNotFound, resp.StatusCode)
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return nil, false, fmt.Errorf("%w: status=%d body=%s", ErrBadRequest, resp.StatusCode, truncate(raw, 256))
	default:
		// 5xx and any unexpected status: retryable.
		return nil, true, fmt.Errorf("%w: status=%d body=%s", ErrServer, resp.StatusCode, truncate(raw, 256))
	}
}

// idLen is the byte-count of random entropy used to mint an auto-generated
// X-Request-ID; the server's RequestID middleware accepts up to 64 alphanumeric
// characters, so 32 hex chars fits comfortably and leaves room for caller
// prefixes if they want to compose.
const idLen = 16

// generateRequestID mints a 16-byte hex string from crypto/rand. The
// default request-id middleware on the server sanitises and accepts any
// alphanumeric / dash / underscore / dot up to 64 chars — 32 hex chars
// fits comfortably.
func generateRequestID() (string, error) {
	b := make([]byte, idLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// truncate caps the body included in error messages so a hostile server
// can't blow up our logs with a 10MB error response. Strip credentials
// first so a server-echoed Bearer token doesn't leak into operator logs.
func truncate(b []byte, n int) string {
	r := redactCredentials(b)
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "...(truncated)"
}
