package assettransferclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

type UploadResponse struct {
	UploadID string `json:"upload_id"`
	URL      string `json:"url,omitempty"`
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *Client) Download(ctx context.Context, assetID string) (io.ReadCloser, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/v1/worker-assets/"+url.PathEscape(assetID)+"/download", nil)
	if err != nil {
		return nil, "", err
	}
	c.applyAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, "", fmt.Errorf("asset download failed: %s", resp.Status)
	}
	return resp.Body, resp.Header.Get("X-Filename"), nil
}

func (c *Client) Initiate(ctx context.Context, assetID string) (*UploadResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/v1/worker-assets/uploads/initiate", strings.NewReader(`{"asset_id":"`+assetID+`"}`))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.applyAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("asset transfer failed: %s", resp.Status)
	}
	var out UploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Finalize(ctx context.Context, assetID string) error {
	return c.do(ctx, http.MethodPost, "/internal/v1/worker-assets/uploads/finalize", assetID)
}

func (c *Client) UploadFile(ctx context.Context, assetID, filePath string) error {
	init, err := c.Initiate(ctx, assetID)
	if err != nil {
		return err
	}
	if init == nil || init.URL == "" {
		return fmt.Errorf("upload url not returned for asset %s", assetID)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+init.URL, f)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Filename", filepath.Base(filePath))
	c.applyAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("asset upload failed: %s", resp.Status)
	}
	return c.Finalize(ctx, assetID)
}

func (c *Client) do(ctx context.Context, method, path, assetID string) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, strings.NewReader(`{"asset_id":"`+assetID+`"}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.applyAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("asset transfer failed: %s", resp.Status)
	}
	return nil
}

func (c *Client) applyAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}
