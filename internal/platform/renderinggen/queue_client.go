// Package renderinggen provides the HTTP client for the central RenderingGen
// queue service. PipelineGen submits the canonical render plan as the queue's
// overlay spec and polls the job until the certified artifact is published.
package renderinggen

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

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// Client is the HTTP implementation of scriptgen.RenderQueueClient. It speaks
// the central queue's wire contract: POST /jobs (submit) and GET /jobs/{id}.
type Client struct {
	baseURL string
	http    *http.Client
}

// New creates a queue client for the given RenderingGen queue endpoint.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Submit enqueues a job. A 409 (job already exists) is translated to
// scriptgen.ErrJobExists so the enqueuer treats replays as idempotent.
func (c *Client) Submit(ctx context.Context, job scriptgen.RenderQueueJob) error {
	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("renderinggen submit marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jobs", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("renderinggen submit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("renderinggen submit do: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return nil
	case http.StatusConflict:
		return fmt.Errorf("%w: job %s", scriptgen.ErrJobExists, job.ID)
	default:
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("renderinggen submit: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
}

// Get returns the current state of a job, including its artifact once done.
func (c *Client) Get(ctx context.Context, id string) (scriptgen.RenderQueueJob, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/jobs/"+url.PathEscape(id), nil)
	if err != nil {
		return scriptgen.RenderQueueJob{}, fmt.Errorf("renderinggen get request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return scriptgen.RenderQueueJob{}, fmt.Errorf("renderinggen get do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return scriptgen.RenderQueueJob{}, fmt.Errorf("renderinggen get: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var job scriptgen.RenderQueueJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return scriptgen.RenderQueueJob{}, fmt.Errorf("renderinggen get decode: %w", err)
	}
	return job, nil
}

var _ scriptgen.RenderQueueClient = (*Client)(nil)
