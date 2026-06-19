// Package jobbrokerclient provides an HTTP client implementation of the
// job.Broker interface for remote workers.
package jobbrokerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// Client is an HTTP implementation of job.Broker for remote workers.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// New creates a new broker client.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) RegisterWorker(ctx context.Context, cmd job.RegisterWorkerCommand) (*job.WorkerSession, error) {
	var session job.WorkerSession
	if err := c.post(ctx, "/api/workers/register", cmd, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (c *Client) Heartbeat(ctx context.Context, cmd job.HeartbeatCommand) error {
	return c.post(ctx, "/api/workers/heartbeat", cmd, nil)
}

func (c *Client) Claim(ctx context.Context, cmd job.ClaimCommand) (*job.Lease, error) {
	var lease job.Lease
	if err := c.post(ctx, "/api/jobs/claim", cmd, &lease); err != nil {
		return nil, err
	}
	return &lease, nil
}

func (c *Client) Renew(ctx context.Context, cmd job.RenewCommand) (*job.Lease, error) {
	var lease job.Lease
	if err := c.post(ctx, fmt.Sprintf("/api/jobs/%s/renew", cmd.JobID), cmd, &lease); err != nil {
		return nil, err
	}
	return &lease, nil
}

func (c *Client) Progress(ctx context.Context, cmd job.ProgressCommand) error {
	return c.post(ctx, fmt.Sprintf("/api/jobs/%s/progress", cmd.JobID), cmd, nil)
}

func (c *Client) Complete(ctx context.Context, cmd job.CompleteCommand) error {
	return c.post(ctx, fmt.Sprintf("/api/jobs/%s/complete", cmd.JobID), cmd, nil)
}

func (c *Client) Fail(ctx context.Context, cmd job.FailCommand) error {
	return c.post(ctx, fmt.Sprintf("/api/jobs/%s/fail", cmd.JobID), cmd, nil)
}

func (c *Client) IsCancelled(ctx context.Context, jobID, leaseID string) (bool, error) {
	url := fmt.Sprintf("%s/api/jobs/%s/cancelled?lease_id=%s", c.baseURL, jobID, leaseID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, err
	}
	c.setAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("is-cancelled: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Cancelled bool `json:"cancelled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, err
	}
	return body.Cancelled, nil
}

func (c *Client) post(ctx context.Context, path string, reqBody any, respBody any) error {
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if respBody != nil {
		if err := json.NewDecoder(resp.Body).Decode(respBody); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (c *Client) setAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}
