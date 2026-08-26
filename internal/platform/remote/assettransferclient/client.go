// Package assettransferclient provides an HTTP client implementation of the
// worker.AssetClient interface for remote workers.
package assettransferclient

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	remoteshared "github.com/Marcuss-ops/PipelineGen/internal/platform/remote/shared"
)

// Client is an HTTP implementation of worker.AssetClient for remote workers.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// New creates a new asset transfer client.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 0, // no timeout for large file transfers
		},
	}
}

// ----- Path constants (single source of truth in shared package) -----------
//
// All paths derive from `remoteshared.InternalPathPrefix` so the
// client and the server's `/internal/v1` router group cannot drift.
// Updating the prefix in one place but not the other surfaces as 404s
// with no breadcrumb — keep them synchronized.

const (
	pathDownloadFmt      = remoteshared.InternalPathPrefix + "/worker-assets/%s/download"
	pathUploadInitiate   = remoteshared.InternalPathPrefix + "/worker-assets/uploads/initiate"
	pathUploadContentFmt = remoteshared.InternalPathPrefix + "/worker-assets/uploads/%s/content"
	pathUploadFinalize   = remoteshared.InternalPathPrefix + "/worker-assets/uploads/finalize"
)

// Download fetches an asset from the server and returns a reader, filename, and error.
func (c *Client) Download(ctx context.Context, assetID string) (io.ReadCloser, string, error) {
	path := fmt.Sprintf(pathDownloadFmt, assetID)
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create download request: %w", err)
	}
	c.setAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download request: %w", err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("download HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	filename := resp.Header.Get("X-Filename")
	if filename == "" {
		filename = assetID
	}
	return resp.Body, filename, nil
}

// UploadFile uploads a local file to the server as an asset.
//
// Three-step handshake: initiate → upload content → finalize. The
// upload-content step passes the filename as BOTH a query parameter
// (?filename=…) AND an X-Filename header so the server can recover
// the original name from either source. The query parameter is
// percent-escaped via url.QueryEscape to survive filenames with
// spaces, ampersands, hashes, and non-ASCII characters (clip names
// routinely contain " - " or "café"). Without escaping, such
// filenames would 400 the request silently break the upload pipeline.
func (c *Client) UploadFile(ctx context.Context, assetID, filePath string) error {
	// Step 1: Initiate upload
	initBody := fmt.Sprintf(`{"asset_id":"%s"}`, assetID)
	if _, err := c.doPost(ctx, pathUploadInitiate, strings.NewReader(initBody)); err != nil {
		return fmt.Errorf("initiate upload: %w", err)
	}

	// Step 2: Upload file content
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file for upload: %w", err)
	}
	defer f.Close()

	filename := filepath.Base(filePath)
	// SECURITY: filename is interpolated into the URL query string.
	// Without url.QueryEscape, a filename like "café voiceover.mp3"
	// produces a malformed URL (raw UTF-8 bytes), a filename like
	// "a&b.mp3" introduces a fake second query parameter, and a
	// filename like "a#b.mp3" would be truncated to "a" by fragment
	// parsing. All three are silent upload failures. Encoded form
	// is canonical; the server already accepts percent-encoded names.
	safeFilename := url.QueryEscape(filename)
	contentPath := fmt.Sprintf(pathUploadContentFmt, assetID) + "?filename=" + safeFilename
	contentURL := c.baseURL + contentPath

	req, err := http.NewRequestWithContext(ctx, "POST", contentURL, f)
	if err != nil {
		return fmt.Errorf("create upload request: %w", err)
	}
	c.setAuth(req)
	req.Header.Set("X-Filename", filename)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload content: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload content HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Step 3: Finalize upload
	finalizeBody := fmt.Sprintf(`{"asset_id":"%s"}`, assetID)
	if _, err := c.doPost(ctx, pathUploadFinalize, strings.NewReader(finalizeBody)); err != nil {
		return fmt.Errorf("finalize upload: %w", err)
	}

	return nil
}

func (c *Client) doPost(ctx context.Context, path string, body io.Reader) ([]byte, error) {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}
	return respBytes, nil
}

func (c *Client) setAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}
