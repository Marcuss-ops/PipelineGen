// extended.go — the surfaces the velox CLI needs on top of the original
// submit/poll pair: a pre-serialized submit with an explicit Idempotency-Key,
// a JSON POST that returns the raw body (search), and a streaming POST that
// writes the response to a destination (download).
//
// They live in the same package so the CLI never re-implements headers,
// timeouts or credential redaction by hand.
package veloxclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// SubmitBytes POSTs an already-serialized JSON payload with an explicit
// Idempotency-Key. Unlike SubmitAsync it never generates a random key, so a
// caller can make a retry deterministic by reusing the same key — the whole
// point of the idempotency contract.
func (c *Client) SubmitBytes(ctx context.Context, path string, payload []byte, idempotencyKey string) (*AsyncResponse, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, fmt.Errorf("veloxclient: SubmitBytes requires a non-empty Idempotency-Key")
	}
	url := c.baseURL + "/" + strings.TrimLeft(path, "/")
	result, err := retry.DoWithValue(ctx, func() (*AsyncResponse, error) {
		raw, retryable, reqErr := c.doRequest(ctx, http.MethodPost, url, payload, idempotencyKey)
		if reqErr != nil {
			if retryable {
				return nil, reqErr
			}
			return nil, reqErr
		}
		var ar AsyncResponse
		if uerr := json.Unmarshal(raw.body, &ar); uerr != nil {
			return nil, fmt.Errorf("veloxclient: decode response: %w (body=%s)", uerr, truncate(raw.body, 256))
		}
		return &ar, nil
	}, c.retryOpts)
	return result, err
}

// PostJSON POSTs payload as JSON and returns the raw response body. Used for
// endpoints whose response is a dynamic envelope (e.g. POST /api/media/search)
// where a dedicated typed method would have to duplicate the wire shape.
func (c *Client) PostJSON(ctx context.Context, path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("veloxclient: marshal payload: %w", err)
	}
	url := c.baseURL + "/" + strings.TrimLeft(path, "/")
	raw, _, err := c.doRequest(ctx, http.MethodPost, url, body, "")
	if err != nil {
		return nil, err
	}
	return raw.body, nil
}

// PostToWriter POSTs payload as JSON and streams the response body into w,
// returning the response Content-Type. This is the download path: the endpoint
// is POST-only (a GET returns 404), which is exactly the mistake the CLI exists
// to prevent.
func (c *Client) PostToWriter(ctx context.Context, path string, payload any, w io.Writer) (string, error) {
	var reqBody io.Reader
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("veloxclient: marshal payload: %w", err)
		}
		reqBody = bytes.NewReader(body)
	}
	url := c.baseURL + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return "", fmt.Errorf("veloxclient: build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrServer, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "", fmt.Errorf("%w: status=%d", ErrUnauthorized, resp.StatusCode)
		case http.StatusNotFound:
			return "", fmt.Errorf("%w: status=%d", ErrNotFound, resp.StatusCode)
		case http.StatusConflict:
			// 409 carries a useful body (e.g. {"status":"RUNNING"}) — surface
			// it so the caller can tell "not ready yet" from "not found".
			return "", fmt.Errorf("%w: status=%d body=%s", ErrNotReady, resp.StatusCode, truncate(raw, 256))
		default:
			return "", fmt.Errorf("%w: status=%d body=%s", ErrBadRequest, resp.StatusCode, truncate(raw, 256))
		}
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return resp.Header.Get("Content-Type"), fmt.Errorf("veloxclient: stream response: %w", err)
	}
	return resp.Header.Get("Content-Type"), nil
}

// IsTerminalStatus reports whether status (case-insensitive) is a terminal job
// state, and whether it is a SUCCESS terminal state. The server emits both the
// legacy lowercase vocabulary (queued/running/completed) and the uppercase
// worker vocabulary (SUCCEEDED/FAILED), so callers must not compare raw
// strings.
func IsTerminalStatus(status string) (terminal bool, success bool) {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCEEDED", "COMPLETED", "DONE", "SUCCESS":
		return true, true
	case "FAILED", "CANCELLED", "CANCELED", "ERROR", "DEAD":
		return true, false
	default:
		return false, false
	}
}
