package veloxclient

// stream.go adds GET streaming: the M2M download and artifact routes return
// bytes, not JSON, and their responses can take minutes / gigabytes. The
// standard Client uses a 30s timeout and buffers JSON bodies, so streaming has
// its own no-timeout client and surfaces the headers the CLI verifies
// (X-Content-SHA256, Content-Range, Content-Type).

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// StreamResult is the metadata of a completed GET stream.
type StreamResult struct {
	ContentType   string
	SHA256        string
	ContentRange  string
	ContentSource string
	Status        int
	Bytes         int64
}

// GetToWriter streams a GET response into w. A Range header is forwarded when
// supplied. It does not retry: a partially written stream must be resumed by
// the caller, not silently restarted.
func (c *Client) GetToWriter(ctx context.Context, path string, headers map[string]string, w io.Writer) (StreamResult, error) {
	url := c.baseURL + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return StreamResult{}, fmt.Errorf("veloxclient: build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "*/*")
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}

	// The standard httpClient has a 30s timeout, which would cut a large
	// render mid-stream. Reuse its transport without the deadline.
	streamClient := &http.Client{Transport: c.httpClient.Transport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return StreamResult{}, fmt.Errorf("%w: %v", ErrServer, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return StreamResult{}, fmt.Errorf("%w: status=%d body=%s", ErrUnauthorized, resp.StatusCode, truncate(raw, 256))
		case http.StatusNotFound:
			return StreamResult{}, fmt.Errorf("%w: status=%d", ErrNotFound, resp.StatusCode)
		case http.StatusConflict, http.StatusUnprocessableEntity:
			return StreamResult{}, fmt.Errorf("%w: status=%d body=%s", ErrNotReady, resp.StatusCode, truncate(raw, 256))
		case http.StatusTooManyRequests:
			return StreamResult{}, fmt.Errorf("%w: status=%d body=%s", ErrRateLimited, resp.StatusCode, truncate(raw, 256))
		default:
			return StreamResult{}, fmt.Errorf("%w: status=%d body=%s", ErrBadRequest, resp.StatusCode, truncate(raw, 256))
		}
	}

	written, copyErr := io.Copy(w, resp.Body)
	result := StreamResult{
		ContentType:   resp.Header.Get("Content-Type"),
		SHA256:        strings.ToLower(strings.TrimSpace(resp.Header.Get("X-Content-SHA256"))),
		ContentRange:  resp.Header.Get("Content-Range"),
		ContentSource: resp.Header.Get("X-Content-Source"),
		Status:        resp.StatusCode,
		Bytes:         written,
	}
	if copyErr != nil {
		return result, fmt.Errorf("veloxclient: stream response: %w", copyErr)
	}
	return result, nil
}
