package filesystem

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job/workspace"
)

// HTTPFetcher is the production workspace.Fetcher over net/http.
// 30-second default timeout matches the canonical worker HTTP timeout
// (godlike/06 forward-prevention: no implicit long-running fetches
// without a deadline).
type HTTPFetcher struct {
	client *http.Client
}

// NewHTTPFetcher constructs the default HTTP fetcher.
func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{client: &http.Client{Timeout: 30 * time.Second}}
}

// compile-time assertion: HTTPFetcher satisfies the kernel Fetcher port.
var _ workspace.Fetcher = (*HTTPFetcher)(nil)

func (f *HTTPFetcher) Fetch(ctx context.Context, url string) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("fetcher: build request %q: %w", url, err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetcher: do request %q: %w", url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, 0, fmt.Errorf("fetcher: non-2xx status %d for %q", resp.StatusCode, url)
	}
	var sizeHint int64
	if resp.ContentLength > 0 {
		sizeHint = resp.ContentLength
	}
	return resp.Body, sizeHint, nil
}

// NewManager wires the production WorkspaceManager: an OS filesystem +
// an HTTP fetcher injected into the kernel's canonical implementation.
// The globalRoot is the parent of every per-job workspace tree; the
// manager creates it if missing.
func NewManager(globalRoot string) (workspace.WorkspaceManager, error) {
	return workspace.NewManagerWithDeps(globalRoot, NewHTTPFetcher(), NewOS())
}
