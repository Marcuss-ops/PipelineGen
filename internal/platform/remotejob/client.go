// Package remotejob submits explicit final_job requests to the Master.
package remotejob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	BaseURL      string
	Token        string
	HTTP         *http.Client
	PollInterval time.Duration
	PollTimeout  time.Duration
}

type Result struct {
	JobID       string
	Status      string
	WorkerID    string
	ArtifactURL string
	SHA256      string
}

type jobEnvelope struct {
	Job      json.RawMessage `json:"job"`
	ID       string          `json:"id"`
	JobID    string          `json:"job_id"`
	Status   string          `json:"status"`
	WorkerID string          `json:"worker_id"`
	Artifact json.RawMessage `json:"artifact"`
	Result   json.RawMessage `json:"result"`
	Error    string          `json:"error"`
}

func New(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), Token: strings.TrimSpace(token), HTTP: &http.Client{Timeout: 45 * time.Second}, PollInterval: 3 * time.Second, PollTimeout: 2 * time.Hour}
}

func ReadTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read remote master credentials: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "VELOX_M2M_SECRET" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("remote master credentials file has no VELOX_M2M_SECRET")
}

// ErrRemotePending is the sentinel for a Master job that EXISTS and is still
// running. It is a sentinel rather than a message so a caller can hand the wait
// to another owner (a later attempt, another worker, a deferral) without
// matching on status strings, and it always travels with the Result that
// carries the job id — the handle a caller must keep in order to come back.
var ErrRemotePending = errors.New("remote job is not terminal yet")

func (c *Client) ready() error {
	if c == nil || c.HTTP == nil || c.BaseURL == "" || c.Token == "" {
		return fmt.Errorf("remote job client is not configured")
	}
	return nil
}

// Prepare performs the Master's PREPARE phase and returns the job it created.
//
// It is split out of Submit so a caller can persist the job id BEFORE waiting
// on it: the id is the only handle to a render that outlives this process, and
// while it lived inside one blocking call a crash mid-render lost the address
// of a job the Master kept running.
func (c *Client) Prepare(ctx context.Context, pre map[string]any) (string, error) {
	if err := c.ready(); err != nil {
		return "", err
	}
	var prepared jobEnvelope
	if err := c.request(ctx, http.MethodPost, "/api/v1/jobs/pre", pre, &prepared, http.StatusAccepted); err != nil {
		return "", fmt.Errorf("remote PREPARE: %w", err)
	}
	jobID := strings.TrimSpace(prepared.JobID)
	if jobID == "" {
		jobID = strings.TrimSpace(prepared.ID)
	}
	if jobID == "" {
		return "", fmt.Errorf("remote PREPARE returned no job_id")
	}
	return jobID, nil
}

// Finalize performs the Master's FINALIZE phase for an already prepared job.
// Idempotent-by-job-id on the caller's side: the phase names the job, so a
// retried FINALIZE addresses the same Master job instead of creating one.
func (c *Client) Finalize(ctx context.Context, jobID string, finalize map[string]any) error {
	if err := c.ready(); err != nil {
		return err
	}
	if strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("remote FINALIZE requires a job id")
	}
	if err := c.request(ctx, http.MethodPost, "/api/v1/jobs/"+url.PathEscape(jobID)+"/finalize", finalize, nil, http.StatusAccepted); err != nil {
		return fmt.Errorf("remote FINALIZE job %s: %w", jobID, err)
	}
	return nil
}

// Poll reads the job's state ONCE, which is what makes the wait hand-off-able:
// one call is a decision point (done / failed / still running) instead of a
// loop that owns the caller's goroutine until the render finishes.
//
//   - terminal success -> (result, nil)
//   - terminal failure -> (result, error)
//   - still running    -> (result carrying the RAW status, ErrRemotePending)
func (c *Client) Poll(ctx context.Context, jobID string) (Result, error) {
	if err := c.ready(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(jobID) == "" {
		return Result{}, fmt.Errorf("remote poll requires a job id")
	}
	var state jobEnvelope
	if err := c.request(ctx, http.MethodGet, "/api/v1/jobs/"+url.PathEscape(jobID), nil, &state, http.StatusOK); err != nil {
		return Result{JobID: jobID}, fmt.Errorf("poll remote job %s: %w", jobID, err)
	}
	state = unwrap(state)
	switch strings.ToUpper(strings.TrimSpace(state.Status)) {
	case "SUCCEEDED", "SUCCESS", "COMPLETED":
		state.Status = "SUCCEEDED"
		return resultFrom(jobID, state), nil
	case "FAILED", "CANCELED", "CANCELLED", "REJECTED":
		return resultFrom(jobID, state), fmt.Errorf("remote job %s ended %s: %s", jobID, state.Status, state.Error)
	}
	return resultFrom(jobID, state), fmt.Errorf("remote job %s is still %s: %w", jobID, state.Status, ErrRemotePending)
}

// Attach waits for a job that was ALREADY submitted: the poll phase of the
// split, usable with no PREPARE/FINALIZE and therefore resumable from a job id
// alone.
//
// budget bounds the wait. budget <= 0 waits up to PollTimeout, which is what
// Submit has always done; a positive budget is the caller saying "hold my
// worker for at most this long, then give me the handle back". Expiry is NOT a
// failure: it returns the last observed Result plus ErrRemotePending, so the
// caller decides whether to yield, defer, or poll again.
func (c *Client) Attach(ctx context.Context, jobID string, budget time.Duration) (Result, error) {
	if err := c.ready(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(jobID) == "" {
		return Result{}, fmt.Errorf("remote attach requires a job id")
	}
	wait := budget
	if wait <= 0 {
		wait = c.PollTimeout
	}
	interval := c.PollInterval
	if interval <= 0 {
		interval = 3 * time.Second
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var last Result
	for {
		result, err := c.Poll(ctx, jobID)
		last = result
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, ErrRemotePending) {
			// Terminal failure or a transport fault: the wait is over either way.
			return result, err
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-deadline.C:
			if budget <= 0 {
				return last, fmt.Errorf("remote job %s exceeded poll timeout: %w", jobID, ErrRemotePending)
			}
			return last, fmt.Errorf("remote job %s still %s after %s: %w", jobID, last.Status, wait, ErrRemotePending)
		case <-ticker.C:
		}
	}
}

// WaitTerminal waits for a job with the client's full poll timeout.
func (c *Client) WaitTerminal(ctx context.Context, jobID string) (Result, error) {
	return c.Attach(ctx, jobID, 0)
}

// Submit performs the Master's PREPARE → FINALIZE handoff and polls the job.
// It is the composition of Prepare, Finalize and WaitTerminal — the split is
// additive: every existing caller keeps the blocking contract it was written
// against, while a caller that needs to yield uses the phases directly.
func (c *Client) Submit(ctx context.Context, pre, finalize map[string]any) (Result, error) {
	jobID, err := c.Prepare(ctx, pre)
	if err != nil {
		return Result{}, err
	}
	if err := c.Finalize(ctx, jobID, finalize); err != nil {
		return Result{JobID: jobID}, err
	}
	return c.WaitTerminal(ctx, jobID)
}

func (c *Client) request(ctx context.Context, method, path string, body any, out any, wantStatus int) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func unwrap(in jobEnvelope) jobEnvelope {
	var wrapped jobEnvelope
	if len(in.Job) > 0 && json.Unmarshal(in.Job, &wrapped) == nil && wrapped.Status != "" {
		return wrapped
	}
	if json.Unmarshal(in.Result, &wrapped) == nil && wrapped.Status != "" {
		return wrapped
	}
	return in
}

func resultFrom(jobID string, state jobEnvelope) Result {
	var artifact struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	}
	_ = json.Unmarshal(state.Artifact, &artifact)
	if artifact.URL == "" {
		_ = json.Unmarshal(state.Result, &artifact)
	}
	return Result{JobID: jobID, Status: state.Status, WorkerID: state.WorkerID, ArtifactURL: artifact.URL, SHA256: artifact.SHA256}
}
