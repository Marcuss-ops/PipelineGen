package jobbrokerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/job"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) RegisterWorker(ctx context.Context, cmd job.RegisterWorkerCommand) (*job.WorkerSession, error) {
	var out job.WorkerSession
	if err := c.post(ctx, "/internal/v1/workers/register", cmd, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Heartbeat(ctx context.Context, cmd job.HeartbeatCommand) error {
	return c.post(ctx, "/internal/v1/workers/heartbeat", cmd, nil)
}

func (c *Client) Claim(ctx context.Context, cmd job.ClaimCommand) (*job.Lease, error) {
	var out job.Lease
	if err := c.post(ctx, "/internal/v1/jobs/claim", cmd, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Renew(ctx context.Context, cmd job.RenewCommand) (*job.Lease, error) {
	var out job.Lease
	path := fmt.Sprintf("/internal/v1/jobs/%s/renew", url.PathEscape(cmd.JobID))
	if err := c.post(ctx, path, cmd, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Progress(ctx context.Context, cmd job.ProgressCommand) error {
	path := fmt.Sprintf("/internal/v1/jobs/%s/progress", url.PathEscape(cmd.JobID))
	return c.post(ctx, path, cmd, nil)
}

func (c *Client) Complete(ctx context.Context, cmd job.CompleteCommand) error {
	path := fmt.Sprintf("/internal/v1/jobs/%s/complete", url.PathEscape(cmd.JobID))
	return c.post(ctx, path, cmd, nil)
}

func (c *Client) Fail(ctx context.Context, cmd job.FailCommand) error {
	path := fmt.Sprintf("/internal/v1/jobs/%s/fail", url.PathEscape(cmd.JobID))
	return c.post(ctx, path, cmd, nil)
}

func (c *Client) IsCancelled(ctx context.Context, jobID string, leaseID string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/v1/jobs/"+url.PathEscape(jobID)+"/cancelled?lease_id="+url.QueryEscape(leaseID), nil)
	if err != nil {
		return false, err
	}
	c.applyAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("jobbrokerclient: %s", strings.TrimSpace(string(body)))
	}
	var out struct {
		Cancelled bool `json:"cancelled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.Cancelled, nil
}

func (c *Client) post(ctx context.Context, path string, in any, out any) error {
	buf := &bytes.Buffer{}
	if in != nil {
		if err := json.NewEncoder(buf).Encode(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, buf)
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
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("jobbrokerclient: %s", strings.TrimSpace(string(body)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) applyAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}
