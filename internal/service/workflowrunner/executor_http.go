package workflowrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpExecutor executes steps that call HTTP endpoints
type httpExecutor struct {
	client *http.Client
}

func newHTTPExecutor() *httpExecutor {
	return &httpExecutor{
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

func (e *httpExecutor) Execute(ctx context.Context, input *StepInput) (*StepOutput, error) {
	step := input.Step
	if step == nil {
		return nil, fmt.Errorf("step is nil in http executor")
	}

	// Parse endpoint - support "METHOD URL" format or just URL (defaults to POST)
	endpoint := step.Endpoint
	method := "POST"
	url := endpoint

	// Check if endpoint starts with HTTP method
	if strings.HasPrefix(endpoint, "GET ") {
		method = "GET"
		url = strings.TrimPrefix(endpoint, "GET ")
	} else if strings.HasPrefix(endpoint, "POST ") {
		method = "POST"
		url = strings.TrimPrefix(endpoint, "POST ")
	} else if strings.HasPrefix(endpoint, "PUT ") {
		method = "PUT"
		url = strings.TrimPrefix(endpoint, "PUT ")
	} else if strings.HasPrefix(endpoint, "DELETE ") {
		method = "DELETE"
		url = strings.TrimPrefix(endpoint, "DELETE ")
	}

	// Render URL with templating
	url = renderTemplate(url, input.Workflow, input.State)

	var reqBody io.Reader
	if len(input.Payload) > 0 && method != "GET" {
		jsonBytes, err := json.Marshal(input.Payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse response as StepOutput
	var output StepOutput
	if err := json.Unmarshal(body, &output); err != nil {
		// If not in expected format, wrap in raw
		output.Raw = map[string]interface{}{
			"status_code": resp.StatusCode,
			"body":        string(body),
		}
		output.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
		output.Status = "completed"
	}

	// If wait is configured, wait for async completion regardless of initial status
	if step.Wait != nil {
		return e.waitForCompletion(ctx, &output, step)
	}

	return &output, nil
}

func (e *httpExecutor) waitForCompletion(ctx context.Context, initial *StepOutput, step *Step) (*StepOutput, error) {
	// Extract run ID from initial response (check RunID field first, then Raw)
	runID := initial.RunID
	if runID == "" && initial.Raw != nil {
		if v, ok := initial.Raw["run_id"].(string); ok {
			runID = v
		}
	}
	if runID == "" {
		return nil, fmt.Errorf("wait requested but run_id missing from step response")
	}

	// Build status URL
	statusURL := step.Wait.StatusEndpoint
	statusURL = strings.ReplaceAll(statusURL, "{{ run_id }}", runID)

	interval := time.Duration(step.Wait.IntervalMS) * time.Millisecond
	if interval == 0 {
		interval = 2 * time.Second
	}
	timeout := time.Duration(step.Wait.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, "GET", statusURL, nil)
		if err != nil {
			return nil, err
		}

		resp, err := e.client.Do(req)
		if err != nil {
			return nil, err
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var statusResp StepOutput
		if err := json.Unmarshal(body, &statusResp); err == nil {
			// Check if success state reached
			for _, s := range step.Wait.Success {
				if statusResp.Status == s {
					return &statusResp, nil
				}
			}
			// Check if failure state reached
			for _, f := range step.Wait.Failure {
				if statusResp.Status == f {
					return nil, fmt.Errorf("step %s failed with status %s", step.ID, f)
				}
			}
		}

		time.Sleep(interval)
	}

	return nil, fmt.Errorf("timeout waiting for step %s to complete", step.ID)
}

// Helper to get run ID from output - defined but not used yet
var _ = func(output *StepOutput) string {
	if id, ok := output.Raw["run_id"].(string); ok {
		return id
	}
	return ""
}
