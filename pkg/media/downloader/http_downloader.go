package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// HTTPDownloader handles direct HTTP downloads (no yt-dlp needed).
type HTTPDownloader struct {
	client *http.Client
}

// NewHTTPDownloader creates a new HTTP downloader.
func NewHTTPDownloader(timeout time.Duration) *HTTPDownloader {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &HTTPDownloader{
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

// HTTPDownloadRequest configures a direct HTTP download.
type HTTPDownloadRequest struct {
	URL        string
	OutputPath string
	MaxSize    int64 // 0 = unlimited
}

// Download performs a direct HTTP download.
func (d *HTTPDownloader) Download(ctx context.Context, req *HTTPDownloadRequest) error {
	if req.URL == "" {
		return fmt.Errorf("URL is required")
	}
	if req.OutputPath == "" {
		return fmt.Errorf("output path is required")
	}

	os.MkdirAll(filepath.Dir(req.OutputPath), 0755)

	out, err := os.Create(req.OutputPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	request, err := http.NewRequestWithContext(ctx, "GET", req.URL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; PipelineGen/1.0)")

	resp, err := d.client.Do(request)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	if req.MaxSize > 0 && resp.ContentLength > req.MaxSize {
		return fmt.Errorf("file too large: %d bytes (max %d)", resp.ContentLength, req.MaxSize)
	}

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// DownloadWithProgress performs download with callback.
func (d *HTTPDownloader) DownloadWithProgress(ctx context.Context, req *HTTPDownloadRequest, progressFn func(downloaded, total int64)) error {
	if req.URL == "" {
		return fmt.Errorf("URL is required")
	}

	request, err := http.NewRequestWithContext(ctx, "GET", req.URL, nil)
	if err != nil {
		return err
	}

	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; PipelineGen/1.0)")

	resp, err := d.client.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	os.MkdirAll(filepath.Dir(req.OutputPath), 0755)
	out, err := os.Create(req.OutputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	if progressFn != nil && resp.ContentLength > 0 {
		_, err = io.Copy(out, &progressReader{r: resp.Body, total: resp.ContentLength, fn: progressFn})
	} else {
		_, err = io.Copy(out, resp.Body)
	}

	return err
}

type progressReader struct {
	r     io.Reader
	total int64
	fn    func(downloaded, total int64)
	n     int64
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.n += int64(n)
	pr.fn(pr.n, pr.total)
	return n, err
}
