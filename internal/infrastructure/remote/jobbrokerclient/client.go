// Package jobbrokerclient provides an HTTP client implementation of the
// appjobs.Broker interface for remote workers.
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

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	remoteshared "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/remote/shared"
)

// Client is an HTTP implementation of appjobs.Broker for remote workers.
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

// ----- Path constants (single source of truth in shared package) -----------
//
// All paths derive from `remoteshared.InternalPathPrefix` so the
// client and the server's `/internal/v1` router group cannot drift.
// Updating the prefix in one place but not the other surfaces as 404s
// with no breadcrumb — keep them synchronized.

const (
	pathRegisterWorker      = remoteshared.InternalPathPrefix + "/workers/register"
	pathHeartbeat           = remoteshared.InternalPathPrefix + "/workers/heartbeat"
	pathClaim               = remoteshared.InternalPathPrefix + "/jobs/claim"
	pathRenewFmt            = remoteshared.InternalPathPrefix + "/jobs/%s/renew"
	pathProgressFmt         = remoteshared.InternalPathPrefix + "/jobs/%s/progress"
	pathCompleteFmt         = remoteshared.InternalPathPrefix + "/jobs/%s/complete"
	pathFailFmt             = remoteshared.InternalPathPrefix + "/jobs/%s/fail"
	pathIsCancelledFmt      = remoteshared.InternalPathPrefix + "/jobs/%s/cancelled"
)

func (c *Client) RegisterWorker(ctx context.Context, cmd appjobs.RegisterWorkerCommand) (*appjobs.WorkerSession, error) {
	var session appjobs.WorkerSession
	if err := c.post(ctx, pathRegisterWorker, cmd, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (c *Client) Heartbeat(ctx context.Context, cmd appjobs.HeartbeatCommand) error {
	return c.post(ctx, pathHeartbeat, cmd, nil)
}

func (c *Client) Claim(ctx context.Context, cmd appjobs.ClaimCommand) (*appjobs.Lease, error) {
	var lease appjobs.Lease
	if err := c.post(ctx, pathClaim, cmd, &lease); err != nil {
		return nil, err
	}
	return &lease, nil
}

func (c *Client) Renew(ctx context.Context, cmd appjobs.RenewCommand) (*appjobs.Lease, error) {
	var lease appjobs.Lease
	if err := c.post(ctx, fmt.Sprintf(pathRenewFmt, cmd.JobID), cmd, &lease); err != nil {
		return nil, err
	}
	return &lease, nil
}

func (c *Client) Progress(ctx context.Context, cmd appjobs.ProgressCommand) error {
	return c.post(ctx, fmt.Sprintf(pathProgressFmt, cmd.JobID), cmd, nil)
}

func (c *Client) Complete(ctx context.Context, cmd appjobs.CompleteCommand) error {
	return c.post(ctx, fmt.Sprintf(pathCompleteFmt, cmd.JobID), cmd, nil)
}

func (c *Client) Fail(ctx context.Context, cmd appjobs.FailCommand) error {
	return c.post(ctx, fmt.Sprintf(pathFailFmt, cmd.JobID), cmd, nil)
}

func (c *Client) IsCancelled(ctx context.Context, jobID, leaseID string) (bool, error) {
	url := fmt.Sprintf("%s%s?lease_id=%s", c.baseURL, fmt.Sprintf(pathIsCancelledFmt, jobID), leaseID)
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
