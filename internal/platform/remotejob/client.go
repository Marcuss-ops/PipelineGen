// Package remotejob submits explicit final_job requests to the Master.
package remotejob

import (
	"bytes"
	"context"
	"encoding/json"
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

// Submit performs the Master's PREPARE → FINALIZE handoff and polls the job.
// The local script-generation runner owns this orchestration after it receives
// a script.generate request with final_job=true; callers do not submit phases.
func (c *Client) Submit(ctx context.Context, pre, finalize map[string]any) (Result, error) {
	if c == nil || c.HTTP == nil || c.BaseURL == "" || c.Token == "" {
		return Result{}, fmt.Errorf("remote job client is not configured")
	}
	var prepared jobEnvelope
	if err := c.request(ctx, http.MethodPost, "/api/v1/jobs/pre", pre, &prepared, http.StatusAccepted); err != nil {
		return Result{}, fmt.Errorf("remote PREPARE: %w", err)
	}
	jobID := strings.TrimSpace(prepared.JobID)
	if jobID == "" {
		jobID = strings.TrimSpace(prepared.ID)
	}
	if jobID == "" {
		return Result{}, fmt.Errorf("remote PREPARE returned no job_id")
	}
	if err := c.request(ctx, http.MethodPost, "/api/v1/jobs/"+url.PathEscape(jobID)+"/finalize", finalize, nil, http.StatusAccepted); err != nil {
		return Result{JobID: jobID}, fmt.Errorf("remote FINALIZE job %s: %w", jobID, err)
	}
	deadline := time.NewTimer(c.PollTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(c.PollInterval)
	defer ticker.Stop()
	for {
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
		select {
		case <-ctx.Done():
			return Result{JobID: jobID}, ctx.Err()
		case <-deadline.C:
			return Result{JobID: jobID}, fmt.Errorf("remote job %s exceeded poll timeout", jobID)
		case <-ticker.C:
		}
	}
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
