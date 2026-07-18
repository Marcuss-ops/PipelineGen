package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// APIClient is a thin HTTP client for calling the PipelineGen API
// with the admin token. It never exposes the token to the browser.
type APIClient struct {
	baseURL    string
	adminToken string
	httpClient *http.Client
}

// NewAPIClient creates a new API client for the given base URL and admin token.
func NewAPIClient(baseURL, adminToken string) *APIClient {
	return &APIClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		adminToken: adminToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// get performs an authenticated GET request and decodes the JSON response.
func (c *APIClient) get(ctx context.Context, path string, params url.Values, out any) error {
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("api client: create request: %w", err)
	}
	if c.adminToken != "" {
		req.Header.Set("X-Velox-Admin-Token", c.adminToken)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("api client: execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api client: %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("api client: decode response: %w", err)
		}
	}
	return nil
}

// post performs an authenticated POST request.
func (c *APIClient) post(ctx context.Context, path string, body any) error {
	u := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("api client: marshal body: %w", err)
		}
		bodyReader = strings.NewReader(string(data))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bodyReader)
	if err != nil {
		return fmt.Errorf("api client: create request: %w", err)
	}
	if c.adminToken != "" {
		req.Header.Set("X-Velox-Admin-Token", c.adminToken)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("api client: execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api client: %s returned %d: %s", path, resp.StatusCode, string(bodyBytes))
	}
	return nil
}

// stream performs an authenticated GET request and returns the response body.
func (c *APIClient) stream(ctx context.Context, path string) (io.ReadCloser, string, error) {
	u := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", fmt.Errorf("api client: create request: %w", err)
	}
	if c.adminToken != "" {
		req.Header.Set("X-Velox-Admin-Token", c.adminToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("api client: execute request: %w", err)
	}

	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, "", fmt.Errorf("api client: %s returned %d", path, resp.StatusCode)
	}

	return resp.Body, resp.Header.Get("Content-Type"), nil
}

// GetDiagnostics calls GET /api/media/diagnostics.
func (c *APIClient) GetDiagnostics(ctx context.Context) (*DiagnosticsResponse, error) {
	var result struct {
		OK       bool           `json:"ok"`
		Degraded bool           `json:"degraded"`
		Checks   map[string]any `json:"checks"`
	}
	if err := c.get(ctx, "/api/media/diagnostics", nil, &result); err != nil {
		return nil, err
	}
	return &DiagnosticsResponse{OK: result.OK, Degraded: result.Degraded, Checks: result.Checks}, nil
}

// GetIndexHealth calls GET /api/media/index-health.
func (c *APIClient) GetIndexHealth(ctx context.Context) (*IndexHealthResponse, error) {
	var result struct {
		OK          bool           `json:"ok"`
		Degraded    bool           `json:"degraded"`
		IndexHealth any            `json:"index_health"`
		AssetStats  map[string]any `json:"asset_stats"`
	}
	if err := c.get(ctx, "/api/media/index-health", nil, &result); err != nil {
		return nil, err
	}
	return &IndexHealthResponse{
		OK:          result.OK,
		Degraded:    result.Degraded,
		IndexHealth: result.IndexHealth,
		AssetStats:  result.AssetStats,
	}, nil
}

// GetJobStats calls GET /api/jobs/stats.
func (c *APIClient) GetJobStats(ctx context.Context) (*JobStatsResponse, error) {
	var result struct {
		Stats map[string]int64 `json:"stats"`
	}
	if err := c.get(ctx, "/api/jobs/stats", nil, &result); err != nil {
		return nil, err
	}
	return &JobStatsResponse{Stats: result.Stats}, nil
}

// GetOutboxStatus calls GET /api/operator/outbox/status (admin endpoint).
func (c *APIClient) GetOutboxStatus(ctx context.Context) (*OutboxStatusResponse, error) {
	var result OutboxStatusResponse
	if err := c.get(ctx, "/api/operator/outbox/status", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetOutboxEvents calls GET /api/operator/outbox/events (admin endpoint).
func (c *APIClient) GetOutboxEvents(ctx context.Context) (*OutboxEventsResponse, error) {
	var result OutboxEventsResponse
	if err := c.get(ctx, "/api/operator/outbox/events", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListAssets calls GET /api/operator/assets with filters.
func (c *APIClient) ListAssets(ctx context.Context, filter AssetFilter) (*AssetListResponse, error) {
	params := url.Values{}
	if filter.Source != "" {
		params.Set("source", filter.Source)
	}
	if filter.MediaType != "" {
		params.Set("media_type", filter.MediaType)
	}
	if filter.LifecycleState != "" {
		params.Set("lifecycle_state", filter.LifecycleState)
	}
	if filter.Category != "" {
		params.Set("category", filter.Category)
	}
	if filter.Q != "" {
		params.Set("q", filter.Q)
	}
	if filter.Limit > 0 {
		params.Set("limit", strconv.Itoa(filter.Limit))
	} else {
		params.Set("limit", "50")
	}
	if filter.Cursor != "" {
		params.Set("cursor", filter.Cursor)
	}
	if filter.HasLocal != nil {
		params.Set("has_local", strconv.FormatBool(*filter.HasLocal))
	}
	if filter.HasDrive != nil {
		params.Set("has_drive", strconv.FormatBool(*filter.HasDrive))
	}

	var result AssetListResponse
	if err := c.get(ctx, "/api/operator/assets", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetAsset calls GET /api/operator/assets/:id.
func (c *APIClient) GetAsset(ctx context.Context, id string) (*AssetDetailResponse, error) {
	var result AssetDetailResponse
	if err := c.get(ctx, "/api/operator/assets/"+url.PathEscape(id), nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// StreamPreview returns the preview body for an asset.
func (c *APIClient) StreamPreview(ctx context.Context, id string) (io.ReadCloser, string, error) {
	return c.stream(ctx, "/api/operator/assets/"+url.PathEscape(id)+"/preview")
}

// ListJobs calls GET /api/jobs with filters.
func (c *APIClient) ListJobs(ctx context.Context, filter JobFilter) (*JobListResponse, error) {
	params := url.Values{}
	if filter.Status != "" {
		params.Set("status", filter.Status)
	}
	if filter.Type != "" {
		params.Set("type", filter.Type)
	}
	if filter.Limit > 0 {
		params.Set("limit", strconv.Itoa(filter.Limit))
	} else {
		params.Set("limit", "50")
	}
	if filter.Offset > 0 {
		params.Set("offset", strconv.Itoa(filter.Offset))
	}

	var result JobListResponse
	if err := c.get(ctx, "/api/jobs", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetJobFull calls GET /api/jobs/:id/full.
func (c *APIClient) GetJobFull(ctx context.Context, id string) (*JobFullResponse, error) {
	var result JobFullResponse
	if err := c.get(ctx, "/api/jobs/"+url.PathEscape(id)+"/full", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RetryJob calls POST /api/jobs/:id/retry.
func (c *APIClient) RetryJob(ctx context.Context, id string) error {
	return c.post(ctx, "/api/jobs/"+url.PathEscape(id)+"/retry", nil)
}

// CancelJob calls POST /api/jobs/:id/cancel.
func (c *APIClient) CancelJob(ctx context.Context, id string) error {
	return c.post(ctx, "/api/jobs/"+url.PathEscape(id)+"/cancel", nil)
}

// AssetFilter holds query parameters for the asset list endpoint.
type AssetFilter struct {
	Source         string
	MediaType      string
	LifecycleState string
	Category       string
	Q              string
	Limit          int
	Cursor         string
	HasLocal       *bool
	HasDrive       *bool
}

// JobFilter holds query parameters for the job list endpoint.
type JobFilter struct {
	Status string
	Type   string
	Limit  int
	Offset int
}
