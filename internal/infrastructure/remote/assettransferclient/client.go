// Package assettransferclient provides an HTTP client implementation of the
// worker.AssetClient interface for remote workers.
package assettransferclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// Download fetches an asset from the server and returns a reader, filename, and error.
func (c *Client) Download(ctx context.Context, assetID string) (io.ReadCloser, string, error) {
	url := fmt.Sprintf("%s/api/worker-assets/%s/download", c.baseURL, assetID)
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
func (c *Client) UploadFile(ctx context.Context, assetID, filePath string) error {
	// Step 1: Initiate upload
	initBody := fmt.Sprintf(`{"asset_id":"%s"}`, assetID)
	if _, err := c.doPost(ctx, "/api/worker-assets/uploads/initiate", strings.NewReader(initBody)); err != nil {
		return fmt.Errorf("initiate upload: %w", err)
	}

	// Step 2: Upload file content
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file for upload: %w", err)
	}
	defer f.Close()

	filename := filepath.Base(filePath)
	url := fmt.Sprintf("%s/api/worker-assets/uploads/%s/content?filename=%s", c.baseURL, assetID, filename)
	req, err := http.NewRequestWithContext(ctx, "POST", url, f)
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
	if _, err := c.doPost(ctx, "/api/worker-assets/uploads/finalize", strings.NewReader(finalizeBody)); err != nil {
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
