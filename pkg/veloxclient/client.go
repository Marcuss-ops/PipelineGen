// Package veloxclient is a minimal HTTP client for submitting jobs to a
// pipelinegen server from any worker. Uses pkg/retry for backoff instead
// of an inline retry loop.
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
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// Client is an HTTP client for the PipelineGen server.
type Client struct {
	baseURL     string
	token       string
	httpClient  *http.Client
	retryOpts   retry.Options
	insecureTLS bool
}

// Option configures the Client at construction time.
type Option func(*Client)

// WithInsecureTLS disables TLS certificate verification.
func WithInsecureTLS() Option {
	return func(c *Client) { c.insecureTLS = true }
}

// WithTimeout sets the per-request HTTP timeout. Defaults to 30s.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.httpClient.Timeout = d }
}

// WithRetryOptions overrides the retry policy. Default is 3 attempts with
// 200ms→800ms exponential backoff.
func WithRetryOptions(opts retry.Options) Option {
	return func(c *Client) {
		c.retryOpts = opts
		c.retryOpts.IsRetryable = isRetryableError
	}
}

// New builds a Client.
func New(baseURL, token string, opts ...Option) *Client {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		// Canonical default aligns with internal/platform/config/types.go
		// `Server.Port` default (8000, established by the Operational
		// Readiness PR — June 2026 — to free up port 8080 for unrelated
		// services and align with the cross-worker-compose patterns).
		// Operators override via the baseURL argument, env (New caller
		// wires it), or VELOX_MASTER_URL / VELOX_PORT in the shell.
		baseURL = "http://127.0.0.1:8000"
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	c := &Client{
		baseURL:    baseURL,
		token:      strings.TrimSpace(token),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		retryOpts: retry.Options{
			MaxAttempts:    DefaultMaxAttempts,
			InitialBackoff: DefaultRetryBase,
			MaxBackoff:     5 * time.Second,
			BackoffFactor:  2.0,
			IsRetryable:    isRetryableError,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.insecureTLS {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
		c.httpClient.Transport = tr
	}
	return c
}

// isRetryableError returns true for errors that should be retried (server
// errors, network failures). Authorization errors (401/403) and validation
// errors (400/404) are not retryable.
func isRetryableError(err error) bool {
	return errors.Is(err, ErrServer)
}

// SubmitAsync POSTs payload to path and returns the enqueue response.
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

	result, err := retry.DoWithValue(ctx, func() (*AsyncResponse, error) {
		raw, retryable, reqErr := c.doRequest(ctx, http.MethodPost, url, body, reqID)
		if reqErr == nil && !retryable {
			var ar AsyncResponse
			if uerr := json.Unmarshal(raw.body, &ar); uerr != nil {
				return nil, fmt.Errorf("veloxclient: decode response: %w (body=%s)", uerr, truncate(raw.body, 256))
			}
			return &ar, nil
		}
		if !retryable {
			return nil, reqErr
		}
		return nil, reqErr
	}, c.retryOpts)
	return result, err
}

// GetJobStatus GETs /api/jobs/{jobID}/full.
func (c *Client) GetJobStatus(ctx context.Context, jobID string) (*JobStatusResponse, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("veloxclient: empty jobID")
	}
	url := c.baseURL + "/api/jobs/" + jobID + "/full"
	resp, retryable, err := c.doRequest(ctx, http.MethodGet, url, nil, "")
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if retryable {
		return nil, ErrServer
	}
	var out JobStatusResponse
	if err := json.Unmarshal(resp.body, &out); err != nil {
		return nil, fmt.Errorf("veloxclient: decode job status: %w", err)
	}
	return &out, nil
}

type rawResponse struct {
	body []byte
}

var redactPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-+/=]{8,}`),
	regexp.MustCompile(`(?i)token[=:]\s*[A-Za-z0-9._\-+/=]{16,}`),
	regexp.MustCompile(`(?i)(authorization|x-velox-admin-token|x-velox-worker-token)["':= ]+[A-Za-z0-9._\-+/=]{8,}`),
}

func redactCredentials(b []byte) []byte {
	out := b
	for _, p := range redactPatterns {
		out = p.ReplaceAll(out, []byte("[REDACTED]"))
	}
	return out
}

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
		// POST /api/script/generate (P0.B gate): the server rejects
		// submissions whose Idempotency-Key header is missing with
		// 400 IDEMPOTENCY_KEY_REQUIRED. Send the same reqID as the
		// idempotency key so retries replay instead of duplicating.
		if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
			req.Header.Set("Idempotency-Key", reqID)
		}
	}
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
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
		return nil, true, fmt.Errorf("%w: status=%d body=%s", ErrServer, resp.StatusCode, truncate(raw, 256))
	}
}

const idLen = 16

func generateRequestID() (string, error) {
	b := make([]byte, idLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func truncate(b []byte, n int) string {
	r := redactCredentials(b)
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "...(truncated)"
}
