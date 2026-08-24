package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
)

// MediaDownloader implements the application-layer assets.MediaDownloader
// port using a standard net/http client.
type MediaDownloader struct {
	client *http.Client
}

// Compile-time assertion that the concrete adapter implements the port.
var _ assets.MediaDownloader = (*MediaDownloader)(nil)

// NewMediaDownloader creates a new MediaDownloader with the given timeout.
// If timeout is zero, a 90-second default is used.
func NewMediaDownloader(timeout time.Duration) *MediaDownloader {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &MediaDownloader{
		client: &http.Client{Timeout: timeout},
	}
}

// Download fetches the resource at url and returns a ReadCloser for the
// response body. The caller is responsible for closing the reader.
func (d *MediaDownloader) Download(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download media: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("download failed (%d): %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}
