// Package e2e provides end-to-end testing for the Velox API
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TestClient provides a client for E2E testing
type TestClient struct {
	BaseURL string
	Client  *http.Client
}

// NewTestClient creates a new E2E test client
func NewTestClient(baseURL string) *TestClient {
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	return &TestClient{
		BaseURL: baseURL,
		Client: &http.Client{
			Timeout: 10 * time.Minute, // Long timeout for video processing
		},
	}
}

// StockCreateRequest represents a request to create a stock clip
type StockCreateRequest struct {
	VideoURL    string `json:"video_url"`
	Title       string `json:"title"`
	Duration    int    `json:"duration"`
	DriveFolder string `json:"drive_folder"`
}

// StockCreateResponse represents the response from create endpoint
type StockCreateResponse struct {
	OK     bool `json:"ok"`
	Drive  struct {
		URL    string `json:"url"`
		FileID string `json:"file_id"`
	} `json:"drive"`
	TaskID string `json:"task_id"`
}

// CreateClip creates a new stock clip
func (c *TestClient) CreateClip(ctx context.Context, req StockCreateRequest) (*StockCreateResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/stock/create", c.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	var result StockCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &result, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	return &result, nil
}

// SearchRequest represents a search request
type SearchRequest struct {
	Title     string `json:"title"`
	MaxClips  int    `json:"max_clips"`
}

// SearchResponse represents the search response
type SearchResponse struct {
	OK    bool `json:"ok"`
	Clips []struct {
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		URL      string  `json:"url"`
		Duration float64 `json:"duration"`
	} `json:"clips"`
}

// SearchStock searches for stock clips
func (c *TestClient) SearchStock(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/stock/search", c.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	var result SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &result, nil
}

// HealthResponse represents the health check response
type HealthResponse struct {
	OK     bool   `json:"ok"`
	Status string `json:"status"`
}

// Health checks the server health
func (c *TestClient) Health(ctx context.Context) (*HealthResponse, error) {
	url := fmt.Sprintf("%s/health", c.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	var result HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &result, nil
}

// WaitForServer waits for the server to be healthy
func (c *TestClient) WaitForServer(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for server")
		case <-ticker.C:
			_, err := c.Health(ctx)
			if err == nil {
				return nil
			}
		}
	}
}